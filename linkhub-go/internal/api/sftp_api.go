package api

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/auth"
	"linkhub/internal/connector"
)

// RegisterSFTPRoutes SSH 会话的文件传输（SFTP）
// 上传=操作员+；列表/下载=观察者+
func (h *Handler) RegisterSFTPRoutes(r chi.Router) {
	r.Route("/api/sessions/{id}/sftp", func(r chi.Router) {
		r.Group(func(ar chi.Router) {
			ar.Use(h.authMw.User)
			ar.With(h.authMw.RequireRole(auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin)).Get("/list", h.sftpList)
			ar.With(h.authMw.RequireRole(auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin)).Get("/download", h.sftpDownload)
		})
		r.Group(func(ar chi.Router) {
			ar.Use(h.authMw.User)
			ar.With(h.authMw.RequireRole(auth.RoleOperator, auth.RoleAdmin)).Post("/upload", h.sftpUpload)
		})
	})
}

// sftpClientOf 校验会话存在且为 SSH，返回 SFTP 客户端
func (h *Handler) sftpClientOf(w http.ResponseWriter, r *http.Request) (*connector.SSHConnector, int64, bool) {
	id, err := paramID(r, "id")
	if err != nil {
		jsonErr(w, 400, "会话 ID 无效")
		return nil, 0, false
	}
	sess := h.manager.Get(id)
	if sess == nil {
		jsonErr(w, 404, "会话不存在或已关闭")
		return nil, 0, false
	}
	sc, ok := sess.Connector.(*connector.SSHConnector)
	if !ok {
		jsonErr(w, 400, "仅 SSH 会话支持文件传输")
		return nil, 0, false
	}
	return sc, id, true
}

func (h *Handler) sftpList(w http.ResponseWriter, r *http.Request) {
	sc, _, ok := h.sftpClientOf(w, r)
	if !ok {
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
}

// sftpUpload multipart 上传到远端目录
func (h *Handler) sftpUpload(w http.ResponseWriter, r *http.Request) {
	sc, _, ok := h.sftpClientOf(w, r)
	if !ok {
		return
	}
	client, err := sc.SFTP()
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	defer client.Close()
	dir := r.FormValue("path")
	if dir == "" {
		dir = "/"
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		jsonErr(w, 400, "缺少文件字段 file")
		return
	}
	defer file.Close()
	name := header.Filename
	if strings.Contains(name, "/") {
		name = path.Base(name)
	}
	dstPath := path.Join(dir, name)
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
}

// sftpDownload 下载远端文件（附件流）
func (h *Handler) sftpDownload(w http.ResponseWriter, r *http.Request) {
	sc, _, ok := h.sftpClientOf(w, r)
	if !ok {
		return
	}
	client, err := sc.SFTP()
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	defer client.Close()
	p := r.URL.Query().Get("path")
	if p == "" || strings.HasSuffix(p, "/") {
		jsonErr(w, 400, "缺少文件路径")
		return
	}
	f, err := client.Open(p)
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
	auditLog(h, r, "sftp_download", p, fmt.Sprintf("%d bytes", st.Size()))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(p)))
	w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
	io.Copy(w, f)
}
