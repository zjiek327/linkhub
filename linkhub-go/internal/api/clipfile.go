package api

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"linkhub/internal/desktop/clipboard"
)

// 剪贴板文件传输：浏览器同源上传到本节点，集群内流式转发到目标节点落盘，
// 并把文件引用写入目标系统剪贴板（Win CF_HDROP / X11 text/uri-list）。
// 上限可用 LINKHUB_CLIP_FILE_MAX_MB 配置，默认 10MB，封顶 200MB。

const (
	clipFileDefaultMB = 10
	clipFileMaxCapMB  = 200
)

// 大文件经多跳转发不设超时（LAN 内 200MB 秒级~十秒级）
var clipHTTPClient = &http.Client{}

func clipFileMaxMB() int64 {
	n := int64(clipFileDefaultMB)
	if v := os.Getenv("LINKHUB_CLIP_FILE_MAX_MB"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil && x > 0 {
			n = x
		}
	}
	if n > clipFileMaxCapMB {
		n = clipFileMaxCapMB
	}
	return n
}

// 上传落盘目录：LINKHUB_DATA_DIR/clipboard-files（与主配置同源，缺省 ~/.linkhub/clipboard-files）
func clipFileDir() string {
	root, err := os.UserHomeDir()
	if err != nil || root == "" {
		root = "."
	}
	if v := strings.TrimSpace(os.Getenv("LINKHUB_DATA_DIR")); v != "" {
		root = v
	} else {
		root = filepath.Join(root, ".linkhub")
	}
	dir := filepath.Join(root, "clipboard-files")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// 文件名净化：剥路径分量/控制字符/保留字符，防目录穿越，超长截断保扩展名
func sanitizeClipFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	if len(name) > 120 {
		ext := filepath.Ext(name)
		name = name[:120-len(ext)] + ext
	}
	return name
}

// clipLimit 告知前端当前文件上限（MB）
func (h *ClusterHandler) clipLimit(w http.ResponseWriter, _ *http.Request) {
	jsonOut(w, map[string]interface{}{"max_mb": clipFileMaxMB()})
}

// clipFileProxy 对外（前端同源上传）：目标是本节点直接落盘（不依赖集群），否则流式转发
func (h *ClusterHandler) clipFileProxy(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node_id")
	filename := sanitizeClipFilename(r.URL.Query().Get("filename"))
	if node == "" || node == "local" || node == "self" || (h.cluster != nil && node == h.cluster.SelfID()) {
		h.saveClipFile(w, r, filename)
		return
	}
	if !h.requireEnabled(w) {
		return
	}
	h.forwardClipFile(w, r, node, filename, 0)
}

// internalClipFile 内部（X-LinkHub-Token）：目标是自己则落盘，否则继续转发（≤3 跳）
func (h *ClusterHandler) internalClipFile(w http.ResponseWriter, r *http.Request) {
	if !h.checkInternalToken(w, r) {
		return
	}
	target := r.URL.Query().Get("target")
	filename := sanitizeClipFilename(r.URL.Query().Get("filename"))
	hops, _ := strconv.Atoi(r.URL.Query().Get("hops"))
	if target == "" || target == h.cluster.SelfID() {
		h.saveClipFile(w, r, filename)
		return
	}
	h.forwardClipFile(w, r, target, filename, hops)
}

// forwardClipFile 流式转发到下一跳（ResolveRoute 解析直连 peer 或间接 via），不缓冲整包
func (h *ClusterHandler) forwardClipFile(w http.ResponseWriter, r *http.Request, target, filename string, hops int) {
	if hops >= 3 {
		jsonErr(w, 502, "转发跳数超限")
		return
	}
	peer, _, ok := h.cluster.ResolveRoute(target)
	if !ok {
		jsonErr(w, 502, "目标节点不可达: "+target)
		return
	}
	q := url.Values{
		"filename": {filename},
		"target":   {target},
		"hops":     {strconv.Itoa(hops + 1)},
	}.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://"+peer.Address+"/api/cluster/internal/clipboard-file?"+q, r.Body)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	req.Header.Set("X-LinkHub-Token", h.cluster.Token())
	if r.ContentLength > 0 {
		req.ContentLength = r.ContentLength
	}
	resp, err := clipHTTPClient.Do(req)
	if err != nil {
		jsonErr(w, 502, err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// saveClipFile 落盘并写入系统剪贴板；超限 413 并清理半成品文件
func (h *ClusterHandler) saveClipFile(w http.ResponseWriter, r *http.Request, filename string) {
	maxBytes := clipFileMaxMB() << 20
	if r.ContentLength > maxBytes {
		jsonErr(w, 413, fmt.Sprintf("文件超过上限 %d MB", clipFileMaxMB()))
		return
	}
	name := time.Now().Format("20060102-150405") + "_" + filename
	path := filepath.Join(clipFileDir(), name)
	f, err := os.Create(path)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	_, copyErr := io.Copy(f, http.MaxBytesReader(w, r.Body, maxBytes))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		jsonErr(w, 413, fmt.Sprintf("文件超过上限 %d MB 或传输中断", clipFileMaxMB()))
		return
	}
	fi, err := os.Stat(path)
	if err != nil {
		jsonErr(w, 500, err.Error())
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// 写系统剪贴板（文件引用）；失败不阻塞——文件已在盘上，响应里带路径兜底
	clipOK := false
	if board, ok := clipboard.Open(); ok {
		clipOK = board.SetFile(abs)
		board.Close()
	}
	jsonOut(w, map[string]interface{}{"ok": true, "path": abs, "size": fi.Size(), "clip": clipOK})
}
