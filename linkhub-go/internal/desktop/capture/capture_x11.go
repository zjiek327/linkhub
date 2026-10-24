//go:build linux

// X11 采集：xgb（纯 Go X 协议实现，无 cgo）。
// Xwayland(rootless) 的 root 窗口无后备像素，直接 GetImage 会 BadMatch，
// 一律走 CopyArea(root→自建 pixmap) → GetImage(pixmap) 中转。
// 进程级单例连接：xgb 认证读 $XAUTHORITY 环境变量，探测(Probe)与会话(WS)并发
// Open 时 Setenv/恢复会互相污染认证上下文，单例彻底消除该竞态。
package capture

import (
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"

	xgb "github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

type x11Screen struct {
	conn *xgb.Conn
	root xproto.Window
	depth uint8
	pm   xproto.Pixmap   // 中转 pixmap
	gc   xproto.Gcontext
	w, h uint16
	buf  []byte
}

var (
	singletonMu     sync.Mutex
	singleton       *x11Screen
	singletonFailed bool // 首次尝试失败标记（X server 不会凭空出现，避免探测反复重连）
)

// OpenX11 打开（或复用）进程级 X11 采集连接。
// 首次失败会记住（X server 不会凭空出现），避免每 2s 的能力探测反复重连拖慢进程。
func OpenX11() (Screen, bool) {
	singletonMu.Lock()
	defer singletonMu.Unlock()
	if singletonFailed {
		return nil, false
	}
	if singleton != nil {
		return singleton, true
	}
	scr, ok := openX11Once()
	if !ok {
		singletonFailed = true
		return nil, false
	}
	singleton = scr
	return scr, true
}

func openX11Once() (*x11Screen, bool) {
	// Wayland 会话守卫：Xwayland 的 root 无真实桌面像素（合成器独占屏幕），
	// 采集到的只是 X 应用区域甚至空 root——降级为 available 走 VNC 路径
	if ms, _ := filepath.Glob("/run/user/" + strconv.Itoa(os.Getuid()) + "/wayland-*"); len(ms) > 0 {
		return nil, false
	}
	for _, auth := range authCandidates() {
		if auth != "" {
			old, had := os.LookupEnv("XAUTHORITY")
			os.Setenv("XAUTHORITY", auth)
			scr, ok := tryDisplays()
			if had {
				os.Setenv("XAUTHORITY", old)
			} else {
				os.Unsetenv("XAUTHORITY")
			}
			if ok {
				return scr, true
			}
		} else {
			if scr, ok := tryDisplays(); ok {
				return scr, true
			}
		}
	}
	return nil, false
}

// tryDisplays 遍历候选 DISPLAY，第一个能取到像素的胜出
func tryDisplays() (*x11Screen, bool) {
	for _, d := range pickDisplay() {
		c, err := xgb.NewConnDisplay(d)
		if err != nil {
			continue
		}
		setup := xproto.Setup(c)
		if len(setup.Roots) == 0 {
			c.Close()
			continue
		}
		r := setup.Roots[0]
		gcid, _ := c.NewId()
		if err := xproto.CreateGCChecked(c, xproto.Gcontext(gcid), xproto.Drawable(r.Root), 0, nil).Check(); err != nil {
			c.Close()
			continue
		}
		pm, _ := c.NewId()
		if err := xproto.CreatePixmapChecked(c, r.RootDepth, xproto.Pixmap(pm), xproto.Drawable(r.Root),
			r.WidthInPixels, r.HeightInPixels).Check(); err != nil {
			c.Close()
			continue
		}
		s := &x11Screen{conn: c, root: r.Root, depth: r.RootDepth,
			gc: xproto.Gcontext(gcid), pm: xproto.Pixmap(pm),
			w: r.WidthInPixels, h: r.HeightInPixels}
		s.buf = make([]byte, int(s.w)*int(s.h)*4)
		// 首帧验证：确认能真取到像素（普通 X 与 Xwayland 均经 pixmap 中转）
		if _, err := s.grab(); err != nil {
			s.Close()
			continue
		}
		return s, true
	}
	return nil, false
}

// pickDisplay 候选 DISPLAY（$DISPLAY 优先，其次 /tmp/.X11-unix/X*）
func pickDisplay() []string {
	out := []string{}
	if d := os.Getenv("DISPLAY"); d != "" {
		out = append(out, d)
	}
	if ms, _ := filepath.Glob("/tmp/.X11-unix/X*"); len(ms) > 0 {
		for _, m := range ms {
			d := ":" + m[len(m)-1:]
			dup := false
			for _, x := range out {
				if x == d {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, d)
			}
		}
	}
	return out
}

// authCandidates 候选 XAUTHORITY（"" = 现状默认 ~/.Xauthority）
// Xwayland 场景 cookie 在 /run/user/$UID/.mutter-Xwaylandauth.*
func authCandidates() []string {
	out := []string{""}
	if runtime.GOOS == "linux" {
		if ms, _ := filepath.Glob("/run/user/" + strconv.Itoa(os.Getuid()) + "/.mutter-Xwaylandauth.*"); len(ms) > 0 {
			out = append(out, ms...)
		}
	}
	return out
}

func (s *x11Screen) Size() image.Point { return image.Pt(int(s.w), int(s.h)) }

// grab 全屏取像：root --CopyArea--> pixmap --GetImage(ZPixmap)--> 像素。
// Stream 串行调用（ticker 驱动）+ 会话互斥，无需再加锁。
func (s *x11Screen) grab() ([]byte, error) {
	if err := xproto.CopyAreaChecked(s.conn, xproto.Drawable(s.root), xproto.Drawable(s.pm), s.gc,
		0, 0, 0, 0, s.w, s.h).Check(); err != nil {
		return nil, err
	}
	// plane mask 按深度截断（24 位深屏用全 1 mask 是 BadMatch 诱因）
	var mask uint32 = 0xffffffff
	if s.depth < 32 {
		mask = (uint32(1) << s.depth) - 1
	}
	rep, err := xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.pm),
		0, 0, s.w, s.h, mask).Reply()
	if err != nil {
		return nil, err
	}
	n := copy(s.buf, rep.Data)
	return s.buf[:n], nil
}

func (s *x11Screen) Frame(_ []byte) ([]byte, error) { return s.grab() }

// Close 空操作：单例连接随进程存亡（Probe 验证/会话结束都不能真关，否则后续复用全断）
func (s *x11Screen) Close() {}

func open() (Screen, bool) { return OpenX11() }
