package api

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/pkg/sftp"

	"linkhub/internal/auth"
	"linkhub/internal/connector"
)

// RegisterSFTPRoutes SSH 会话的文件传输（SFTP）
// 公开端点：带 node 参数时自动转发到会话归属节点（跨网段经桥接）
func (h *Handler) RegisterSFTPRoutes(r chi.Router) {
	r.Group(func(ar chi.Router) {
		ar.Use(h.authMw.User)
		ar.Route("/api/sessions/{id}/sftp", func(sr chi.Router) {
			sr.With(h.authMw.RequireRole(auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin)).Get("/list", h.sftpList)
			sr.With(h.authMw.RequireRole(auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin)).Get("/download", h.sftpDownload)
			sr.With(h.authMw.RequireRole(auth.RoleOperator, auth.RoleAdmin)).Post("/upload", h.sftpUpload)
		})
	})
	// 节点间内部端点（X-LinkHub-Token）
	r.Route("/api/cluster/internal/sftp", func(sr chi.Router) {
		sr.Get("/list", h.internalSFTPExec("list"))
		sr.Get("/download", h.internalSFTPExec("download"))
		sr.Post("/upload", h.internalSFTPExec("upload"))
	})
}

// sftpSessionConnector 取本节点会话的 SSH 连接器
func (h *Handler) sftpSessionConnector(w http.ResponseWriter, r *http.Request, sid int64) (*connector.SSHConnector, bool) {
	sess := h.manager.Get(sid)
	if sess == nil {
		jsonErr(w, 404, "会话不存在或已关闭")
		return nil, false
	}
	sc, ok := sess.Connector.(*connector.SSHConnector)
	if !ok {
		jsonErr(w, 400, "仅 SSH 会话支持文件传输")
		return nil, false
	}
	return sc, true
}

// trySFTPProxy 会话不在本节点 → 经集群转发到归属节点执行；返回是否已处理
func (h *Handler) trySFTPProxy(w http.ResponseWriter, r *http.Request, sid int64, action string) bool {
	if h.cluster == nil {
		return false
	}
	node := r.URL.Query().Get("node")
	if node == "" || node == h.cluster.SelfID() {
		return false
	}
	peer, _, ok := h.cluster.ResolveRoute(node)
	if !ok || peer.Status != "online" {
		jsonErr(w, 503, "会话归属节点不在线")
		return true
	}
	hops, _ := strconv.Atoi(r.Header.Get("X-SFTP-Hops"))
	if hops > 3 {
		jsonErr(w, 502, "转发跳数超限")
		return true
	}
	q := r.URL.Query()
	q.Set("session_id", strconv.FormatInt(sid, 10))
	q.Set("target", node)
	u := fmt.Sprintf("%s/api/cluster/internal/sftp/%s?%s", peer.Address, action, q.Encode())
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return true
	}
	req.Header.Set("X-LinkHub-Token", h.cluster.Token())
	req.Header.Set("X-SFTP-Hops", strconv.Itoa(hops+1))
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return true
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	return true
}

// internalSFTPExec 节点间 SFTP 执行端点：target=自己 → 本地执行；否则继续转发
func (h *Handler) internalSFTPExec(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cluster == nil {
			jsonErr(w, 404, "集群未启用")
			return
		}
		if r.Header.Get("X-LinkHub-Token") != h.cluster.Token() {
			jsonErr(w, 403, "令牌无效")
			return
		}
		target := r.URL.Query().Get("target")
		sid, err := strconv.ParseInt(r.URL.Query().Get("session_id"), 10, 64)
		if err != nil {
			jsonErr(w, 400, "session_id 无效")
			return
		}
		if target != "" && target != h.cluster.SelfID() {
			hops, _ := strconv.Atoi(r.Header.Get("X-SFTP-Hops"))
			if hops > 3 {
				jsonErr(w, 502, "转发跳数超限")
				return
			}
			peer, _, ok := h.cluster.ResolveRoute(target)
			if !ok || peer.Status != "online" {
				jsonErr(w, 503, "目标节点不可达")
				return
			}
			q := r.URL.Query()
			q.Set("target", target)
			q.Set("session_id", strconv.FormatInt(sid, 10))
			u := fmt.Sprintf("%s/api/cluster/internal/sftp/%s?%s", peer.Address, action, q.Encode())
			req, _ := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
			req.Header.Set("X-LinkHub-Token", h.cluster.Token())
			req.Header.Set("X-SFTP-Hops", strconv.Itoa(hops+1))
			if ct := r.Header.Get("Content-Type"); ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				jsonErr(w, 502, err.Error())
				return
			}
			defer resp.Body.Close()
			for k, vs := range resp.Header {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return
		}
		// 本地执行
		h.sftpExecLocal(w, r, sid, action)
	}
}

func (h *Handler) sftpExecLocal(w http.ResponseWriter, r *http.Request, sid int64, action string) {
	sess := h.manager.Get(sid)
	if sess == nil {
		jsonErr(w, 404, "会话不存在或已关闭")
		return
	}
	sc, ok := sess.Connector.(*connector.SSHConnector)
	if !ok {
		jsonErr(w, 400, "仅 SSH 会话支持文件传输")
		return
	}
	client, err := sc.SFTP()
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	defer client.Close()
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = "/"
	}
	switch action {
	case "list":
		entries, err := client.ReadDir(dir)
		if err != nil {
			jsonErr(w, 502, err.Error())
			return
		}
		type Entry struct {
			Name  string    `json:"name"`
			IsDir bool      `json:"is_dir"`
			Size  int64     `json:"size"`
			Mtime time.Time `json:"mtime"`
		}
		out := []Entry{}
		for _, e := range entries {
			out = append(out, Entry{Name: e.Name(), IsDir: e.IsDir(), Size: e.Size(), Mtime: e.ModTime()})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDir != out[j].IsDir {
				return out[i].IsDir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		jsonOut(w, map[string]interface{}{"path": path.Clean(dir), "entries": out})
	case "upload":
		file, header, err := r.FormFile("file")
		if err != nil {
			jsonErr(w, 400, "缺少文件字段 file")
			return
		}
		defer file.Close()
		name := path.Base(header.Filename)
		// sub=相对子路径（保持目录结构），自动创建父目录
		sub := path.Clean("/" + r.URL.Query().Get("sub"))
		dstPath := path.Join(dir, name)
		if sub != "/" && sub != "." {
			dstPath = path.Join(dir, strings.TrimPrefix(sub, "/"), name)
			if err := sftpMkdirAll(client, path.Dir(dstPath)); err != nil {
				jsonErr(w, 502, "创建目录失败: "+err.Error())
				return
			}
		}
		dst, err := client.Create(dstPath)
		if err != nil {
			jsonErr(w, 502, "创建远端文件失败: "+err.Error())
			return
		}
		defer dst.Close()
		n, err := io.Copy(dst, file)
		if err != nil {
			jsonErr(w, 502, "写入失败: "+err.Error())
			return
		}
		auditLog(h, r, "sftp_upload", dstPath, fmt.Sprintf("%d bytes", n))
		jsonOut(w, map[string]interface{}{"ok": true, "path": dstPath, "size": n})
	case "download":
		if strings.HasSuffix(dir, "/") {
			jsonErr(w, 400, "缺少文件路径")
			return
		}
		f, err := client.Open(dir)
		if err != nil {
			jsonErr(w, 502, err.Error())
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			jsonErr(w, 400, "目标不是文件")
			return
		}
		auditLog(h, r, "sftp_download", dir, fmt.Sprintf("%d bytes", st.Size()))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(dir)))
		w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
		io.Copy(w, f)
	}
}

// ---------- 旧公开端点（保留兼容：本地执行 + node 参数转发） ----------
func (h *Handler) sftpList(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		jsonErr(w, 400, "会话 ID 无效")
		return
	}
	if h.manager.Get(id) == nil {
		if h.trySFTPProxy(w, r, id, "list") {
			return
		}
		jsonErr(w, 404, "会话不存在或已关闭")
		return
	}
	h.sftpExecLocal(w, r, id, "list")
}

func (h *Handler) sftpDownload(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		jsonErr(w, 400, "会话 ID 无效")
		return
	}
	if h.manager.Get(id) == nil {
		if h.trySFTPProxy(w, r, id, "download") {
			return
		}
		jsonErr(w, 404, "会话不存在或已关闭")
		return
	}
	h.sftpExecLocal(w, r, id, "download")
}

func (h *Handler) sftpUpload(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		jsonErr(w, 400, "会话 ID 无效")
		return
	}
	if h.manager.Get(id) == nil {
		if h.trySFTPProxy(w, r, id, "upload") {
			return
		}
		jsonErr(w, 404, "会话不存在或已关闭")
		return
	}
	h.sftpExecLocal(w, r, id, "upload")
}

// sftpMkdirAll 逐级创建远端目录
func sftpMkdirAll(client *sftp.Client, dir string) error {
	if dir == "/" || dir == "." || dir == "" {
		return nil
	}
	if _, err := client.Stat(dir); err == nil {
		return nil
	}
	parent := path.Dir(dir)
	if err := sftpMkdirAll(client, parent); err != nil {
		return err
	}
	return client.Mkdir(dir)
}
