package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/pbkdf2"

	"linkhub/internal/models"
	"linkhub/internal/store"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

type contextKey string

const userKey contextKey = "user"

// HashPassword PBKDF2 密码哈希（与 Python 版格式兼容：salt$hex）
func HashPassword(password, salt string) string {
	if salt == "" {
		salt = randHex(8)
	}
	dk := pbkdf2.Key([]byte(password), []byte(salt), 100000, 32, sha256.New)
	return salt + "$" + base64.StdEncoding.EncodeToString(dk)
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// VerifyPassword 校验（兼容 Python 版 hex 编码）
func VerifyPassword(password, stored string) bool {
	parts := strings.SplitN(stored, "$", 2)
	if len(parts) != 2 {
		return false
	}
	salt, hexDigest := parts[0], parts[1]
	dk := pbkdf2.Key([]byte(password), []byte(salt), 100000, 32, sha256.New)
	return base64.StdEncoding.EncodeToString(dk) == hexDigest
}

// MakeToken 签发 JWT
func MakeToken(u *models.User, secret string) string {
	claims := jwt.MapClaims{
		"sub":  fmt.Sprintf("%d", u.ID),
		"name": u.Name,
		"role": u.Role,
		"exp":  time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte(secret))
	return s
}

// MakeTenantToken 带租户上下文的 token：tid=租户 ID，trole=该租户内角色，
// pa=平台管理员（跨租户）
func MakeTenantToken(u *models.User, secret string, tenantID int64, tenantRole string, platformAdmin bool) string {
	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", u.ID),
		"name":  u.Name,
		"role":  tenantRole,
		"tid":   tenantID,
		"pa":    platformAdmin,
		"exp":   time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, _ := token.SignedString([]byte(secret))
	return s
}

// DecodeToken 解析 JWT
func DecodeToken(tokenStr, secret string) (*jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("无效令牌")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("无效声明")
	}
	return &claims, nil
}

// Crypto AES-GCM 凭证加密
type Crypto struct{ key []byte }

func NewCrypto(secret string) *Crypto {
	h := sha256.Sum256([]byte(secret))
	return &Crypto{key: h[:]}
}

func (c *Crypto) Encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (c *Crypto) Decrypt(enc string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("密文太短")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

// Middleware 认证中间件
type Middleware struct {
	store  *store.Store
	secret string
}

func NewMiddleware(st *store.Store, secret string) *Middleware {
	return &Middleware{store: st, secret: secret}
}

func (m *Middleware) User(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, reject := m.Authenticate(r)
		if reject != "" {
			http.Error(w, `{"detail":"`+reject+`"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate 解析请求 token → 带 User/TenantCtx 的 context（WS 等非中间件路径复用）。
// 返回 (ctx, "") 或 (nil, 拒绝原因)
func (m *Middleware) Authenticate(r *http.Request) (context.Context, string) {
	token := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = h[7:]
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		if c, err := r.Cookie("linkhub_token"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		return nil, "未登录"
	}
	claims, err := DecodeToken(token, m.secret)
	if err != nil {
		return nil, "登录已过期"
	}
	name, _ := (*claims)["name"].(string)
	u, err := m.store.GetUserByName(name)
	if err != nil || u == nil || !u.Enabled {
		return nil, "用户不存在或已禁用"
	}
	// 租户上下文：tid + 成员角色实时校验（角色变更/移除即刻生效）
	tc := &TenantCtx{Platform: m.store.IsPlatformAdmin(u.ID)}
	if tid, ok := (*claims)["tid"].(float64); ok && tid > 0 {
		if mem, _ := m.store.GetMembership(int64(tid), u.ID); mem != nil {
			tc.TenantID = mem.TenantID
			tc.Role = mem.Role
		} else if !tc.Platform {
			return nil, "租户成员关系已失效，请重新登录"
		}
	}
	if tc.Role == "" {
		tc.Role = u.Role // 平台管理员或旧 token 兜底
	}
	ctx := context.WithValue(r.Context(), userKey, u)
	ctx = SetTenant(ctx, tc)
	return ctx, ""
}

func (m *Middleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tc := TenantFrom(r.Context())
			role := ""
			if tc != nil {
				role = tc.Role
			}
			if role == "" {
				if u := UserFrom(r.Context()); u != nil {
					role = u.Role
				}
			}
			// platform_admin/tenant_admin 兼容旧角色体系：等同 admin 并满足 operator/viewer 组
			effective := role
			if role == "platform_admin" || role == "tenant_admin" {
				effective = RoleAdmin
			}
			if effective == RoleAdmin {
				allowed[RoleOperator] = true
				allowed[RoleViewer] = true
			}
			if effective == RoleOperator {
				allowed[RoleViewer] = true
			}
			if !allowed[effective] && !allowed[role] {
				http.Error(w, `{"detail":"权限不足"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(userKey).(*models.User)
	return u
}

type tenantKeyT struct{}

var tenantKey = tenantKeyT{}

// TenantCtx 请求的租户上下文
type TenantCtx struct {
	TenantID int64  // 当前租户（0=平台管理员未选租户）
	Role     string // 该租户内角色（tenant_admin/operator/viewer）
	Platform bool   // 平台管理员
}

func SetTenant(ctx context.Context, t *TenantCtx) context.Context {
	return context.WithValue(ctx, tenantKey, t)
}

func TenantFrom(ctx context.Context) *TenantCtx {
	t, _ := ctx.Value(tenantKey).(*TenantCtx)
	return t
}

// RequirePlatformAdmin 平台管理员
func (m *Middleware) RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t := TenantFrom(r.Context()); t != nil && t.Platform {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, `{"detail":"需要平台管理员权限"}`, http.StatusForbidden)
	})
}

// RequirePlatformAdminHTTP chi 中间件形式：仅平台管理员
func (m *Middleware) RequirePlatformAdminHTTP(next http.Handler) http.Handler {
	return m.RequirePlatformAdmin(next)
}

// RequireTenantAdminHTTP chi 中间件形式：平台管理员或租户管理员
func (m *Middleware) RequireTenantAdminHTTP(next http.Handler) http.Handler {
	return m.RequireTenantAdmin(next)
}

// RequireTenantAdminOf 校验路径租户（/{id}）的管理权：平台管理员或该租户 tenant_admin
func (m *Middleware) RequireTenantAdminOf(idFromRequest func(*http.Request) int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := TenantFrom(r.Context())
		if t == nil {
			http.Error(w, `{"detail":"未登录"}`, http.StatusUnauthorized)
			return
		}
		if t.Platform {
			next.ServeHTTP(w, r)
			return
		}
		if t.Role == "tenant_admin" && t.TenantID == idFromRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, `{"detail":"需要该租户的管理员权限"}`, http.StatusForbidden)
	})
}

// RequireTenantAdmin 平台管理员或租户管理员
func (m *Middleware) RequireTenantAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := TenantFrom(r.Context())
		if t != nil && (t.Platform || t.Role == "tenant_admin") {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, `{"detail":"需要租户管理员权限"}`, http.StatusForbidden)
	})
}

// EnsureDefaultAdmin 首次启动创建 admin/admin
func EnsureDefaultAdmin(st *store.Store) error {
	u, err := st.GetUserByName("admin")
	if err != nil || u != nil {
		return err
	}
	return st.CreateUser(&models.User{
		Name: "admin", PasswordHash: HashPassword("admin", ""), Role: RoleAdmin, Enabled: true,
	})
}
