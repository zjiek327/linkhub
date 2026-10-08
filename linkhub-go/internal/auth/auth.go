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
			http.Error(w, `{"detail":"未登录"}`, http.StatusUnauthorized)
			return
		}
		claims, err := DecodeToken(token, m.secret)
		if err != nil {
			http.Error(w, `{"detail":"登录已过期"}`, http.StatusUnauthorized)
			return
		}
		name, _ := (*claims)["name"].(string)
		u, err := m.store.GetUserByName(name)
		if err != nil || u == nil || !u.Enabled {
			http.Error(w, `{"detail":"用户不存在或已禁用"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u := UserFrom(r.Context())
			if u == nil || !allowed[u.Role] {
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
