//go:build linux

// X11 剪贴板（纯 xgb，无外部进程）：
// Get  = XConvertSelection 协商读 CLIPBOARD（UTF8_STRING）
// Set  = XSetSelectionOwner 成为属主 + goroutine 服务 SelectionRequest 60s
// 注意：Wayland 会话下只能看到 XWayland 应用的剪贴板（原生 Wayland 剪贴板
// 在合成器里，与内置采集的 Wayland 守卫同理，属尽力而为）
package clipboard

import (
	"sync"
	"time"

	xgb "github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

const maxText = 256 * 1024 // 文本上限，超出截断

type x11Board struct{ mu sync.Mutex }

func open() (Board, bool) {
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, false
	}
	conn.Close()
	return &x11Board{}, true
}

func intern(conn *xgb.Conn, name string) xproto.Atom {
	rep, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return xproto.AtomNone
	}
	return rep.Atom
}

// mkWindow 建 1x1 不映射窗口（selection 协商必须有窗口收事件）
func mkWindow(conn *xgb.Conn) (xproto.Window, error) {
	setup := xproto.Setup(conn)
	root := setup.Roots[0]
	wid, err := xproto.NewWindowId(conn)
	if err != nil {
		return 0, err
	}
	if err := xproto.CreateWindowChecked(conn, 0, wid, root.Root,
		0, 0, 1, 1, 0, xproto.WindowClassInputOutput, root.RootVisual, 0, nil).Check(); err != nil {
		return 0, err
	}
	return wid, nil
}

func (b *x11Board) Get() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	conn, err := xgb.NewConn()
	if err != nil {
		return "", false
	}
	defer conn.Close()
	win, err := mkWindow(conn)
	if err != nil {
		return "", false
	}
	clip := intern(conn, "CLIPBOARD")
	utf8 := intern(conn, "UTF8_STRING")
	prop := intern(conn, "LINKHUB_CLIP")
	if clip == xproto.AtomNone || utf8 == xproto.AtomNone || prop == xproto.AtomNone {
		return "", false
	}
	owner, err := xproto.GetSelectionOwner(conn, clip).Reply()
	if err != nil || owner.Owner == xproto.AtomNone || owner.Owner == win {
		return "", false
	}
	// 请求把 CLIPBOARD 内容转成 UTF8_STRING 放进我们的 prop
	xproto.ConvertSelection(conn, win, clip, utf8, prop, xproto.TimeCurrentTime)
	evCh := make(chan xgb.Event, 1)
	errCh := make(chan error, 1)
	go func() {
		ev, err := conn.WaitForEvent()
		if err != nil {
			errCh <- err
			return
		}
		evCh <- ev
	}()
	var sn xproto.SelectionNotifyEvent
	select {
	case ev := <-evCh:
		var ok bool
		sn, ok = ev.(xproto.SelectionNotifyEvent)
		if !ok || sn.Property == xproto.AtomNone {
			return "", false
		}
	case <-errCh:
		return "", false
	case <-time.After(time.Second):
		return "", false
	}
	// 分块读 prop（offset 单位恒为 4 字节）
	var data []byte
	off := uint32(0)
	for {
		rep, err := xproto.GetProperty(conn, true, win, sn.Property,
			xproto.GetPropertyTypeAny, off, 16384).Reply()
		if err != nil {
			return "", false
		}
		data = append(data, rep.Value...)
		if rep.BytesAfter == 0 || len(data) >= maxText {
			break
		}
		off += uint32(len(rep.Value)) / 4
	}
	if len(data) > maxText {
		data = data[:maxText]
	}
	return string(data), true
}

func (b *x11Board) Set(text string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(text) > maxText {
		text = text[:maxText]
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return false
	}
	win, err := mkWindow(conn)
	if err != nil {
		conn.Close()
		return false
	}
	clip := intern(conn, "CLIPBOARD")
	targets := intern(conn, "TARGETS")
	utf8 := intern(conn, "UTF8_STRING")
	if clip == xproto.AtomNone || targets == xproto.AtomNone || utf8 == xproto.AtomNone {
		conn.Close()
		return false
	}
	xproto.SetSelectionOwner(conn, win, clip, xproto.TimeCurrentTime)
	if owner, _ := xproto.GetSelectionOwner(conn, clip).Reply(); owner == nil || owner.Owner != win {
		conn.Close()
		return false
	}
	// 服务取剪贴板请求；60s 后关闭连接自动放弃属主（SelectionClear 同理）
	go serveSelection(conn, win, text, targets, utf8, 0, 0)
	return true
}

// SetFile 把文件引用写入剪贴板：成为 CLIPBOARD 属主，按 text/uri-list 与
// x-special/gnome-copied-files 应答（GTK/Qt/KDE 文件管理器 Ctrl+V 粘贴出文件）
func (b *x11Board) SetFile(absPath string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	conn, err := xgb.NewConn()
	if err != nil {
		return false
	}
	win, err := mkWindow(conn)
	if err != nil {
		conn.Close()
		return false
	}
	clip := intern(conn, "CLIPBOARD")
	targets := intern(conn, "TARGETS")
	utf8 := intern(conn, "UTF8_STRING")
	uri := intern(conn, "text/uri-list")
	gnome := intern(conn, "x-special/gnome-copied-files")
	if clip == xproto.AtomNone || targets == xproto.AtomNone || utf8 == xproto.AtomNone ||
		uri == xproto.AtomNone || gnome == xproto.AtomNone {
		conn.Close()
		return false
	}
	xproto.SetSelectionOwner(conn, win, clip, xproto.TimeCurrentTime)
	if owner, _ := xproto.GetSelectionOwner(conn, clip).Reply(); owner == nil || owner.Owner != win {
		conn.Close()
		return false
	}
	// text=文件绝对路径；uri/gnome 非 0 触发文件模式
	go serveSelection(conn, win, absPath, targets, utf8, uri, gnome)
	return true
}

// serveSelection 响应 SelectionRequest：
//   - 文本模式（uri==0）：TARGETS=[TARGETS,UTF8_STRING] + UTF8_STRING/STRING 数据
//   - 文件模式（uri!=0）：TARGETS 加 text/uri-list、x-special/gnome-copied-files，
//     另以 UTF8_STRING 应答纯路径（无 URI 能力的应用粘贴出路径文本）
//
// 连接关闭即放弃 selection 属主
func serveSelection(conn *xgb.Conn, win xproto.Window, text string, targets, utf8, uri, gnome xproto.Atom) {
	uris, gnomeData := "", ""
	if uri != 0 {
		uris = "file://" + text + "\n"
		gnomeData = "copy\nfile://" + text
	}
	go func() {
		time.Sleep(60 * time.Second)
		conn.Close() // 触发 WaitForEvent 出错退出
	}()
	for {
		ev, err := conn.WaitForEvent()
		if err != nil {
			conn.Close()
			return
		}
		switch e := ev.(type) {
		case xproto.SelectionClearEvent:
			conn.Close()
			return
		case xproto.SelectionRequestEvent:
			prop := xproto.Atom(xproto.AtomNone)
			switch e.Target {
			case targets:
				prop = e.Property
				atoms := []xproto.Atom{targets, utf8}
				if uri != 0 {
					atoms = append(atoms, uri, gnome)
				}
				data := make([]byte, 0, len(atoms)*4)
				for _, a := range atoms {
					data = append(data, byte(a), byte(a>>8), byte(a>>16), byte(a>>24))
				}
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor,
					e.Property, xproto.AtomAtom, 32, uint32(len(atoms)), data)
			case uri:
				prop = e.Property
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor,
					e.Property, uri, 8, uint32(len(uris)), []byte(uris))
			case gnome:
				prop = e.Property
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor,
					e.Property, gnome, 8, uint32(len(gnomeData)), []byte(gnomeData))
			case utf8, xproto.AtomString:
				prop = e.Property
				xproto.ChangeProperty(conn, xproto.PropModeReplace, e.Requestor,
					e.Property, e.Target, 8, uint32(len(text)), []byte(text))
			}
			xproto.SendEvent(conn, false, e.Requestor, 0, string(xproto.SelectionNotifyEvent{
				Time:      e.Time,
				Requestor: e.Requestor,
				Selection: e.Selection,
				Target:    e.Target,
				Property:  prop,
			}.Bytes()))
			_ = win
		}
	}
}

func (b *x11Board) Close() {}
