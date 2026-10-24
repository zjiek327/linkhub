//go:build linux

// X11 输入注入：XTest 扩展（FakeInput）
package input

import (
	"os"
	"path/filepath"
	"sync"

	xgb "github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

type x11Input struct {
	mu   sync.Mutex
	conn *xgb.Conn
	root xproto.Window

	// 键盘映射：keycode → keysym 列表（列0=无修饰，列1=Shift）
	kcMin, kcMax uint8
	col0         map[uint8]uint32
	col1         map[uint8]uint32
	sym2kc       map[uint32]uint8 // 列0 反查
	sym2kcShift  map[uint32]uint8 // 列1 反查（需配合 Shift）
}

func open() (Injector, bool) {
	displays := []string{}
	if d := os.Getenv("DISPLAY"); d != "" {
		displays = append(displays, d)
	}
	if ms, _ := filepath.Glob("/tmp/.X11-unix/X*"); len(ms) > 0 {
		for _, m := range ms {
			displays = append(displays, ":"+m[len(m)-1:])
		}
	}
	for _, d := range displays {
		c, err := xgb.NewConnDisplay(d)
		if err != nil {
			continue
		}
		if err := xtest.Init(c); err != nil {
			c.Close()
			continue
		}
		setup := xproto.Setup(c)
		if len(setup.Roots) == 0 {
			c.Close()
			continue
		}
		in := &x11Input{conn: c, root: setup.Roots[0].Root,
			kcMin: uint8(setup.MinKeycode), kcMax: uint8(setup.MaxKeycode),
			col0: map[uint8]uint32{}, col1: map[uint8]uint32{},
			sym2kc: map[uint32]uint8{}, sym2kcShift: map[uint32]uint8{}}
		if err := in.loadKeymap(); err != nil {
			c.Close()
			continue
		}
		return in, true
	}
	return nil, false
}

// loadKeymap 读服务端键盘映射，建 keysym→keycode 索引
func (in *x11Input) loadKeymap() error {
	cookie := xproto.GetKeyboardMapping(in.conn, xproto.Keycode(in.kcMin), uint8(in.kcMax-in.kcMin+1))
	rep, err := cookie.Reply()
	if err != nil {
		return err
	}
	perKc := int(rep.KeysymsPerKeycode)
	if perKc < 2 {
		perKc = 2
	}
	for i := 0; i < len(rep.Keysyms); i += perKc {
		kc := in.kcMin + uint8(i/perKc)
		s0 := uint32(rep.Keysyms[i])
		s1 := uint32(0)
		if perKc > 1 {
			s1 = uint32(rep.Keysyms[i+1])
		}
		if s0 != 0 {
			in.col0[kc] = s0
			if _, ok := in.sym2kc[s0]; !ok {
				in.sym2kc[s0] = kc
			}
		}
		if s1 != 0 {
			in.col1[kc] = s1
			if _, ok := in.sym2kcShift[s1]; !ok {
				in.sym2kcShift[s1] = kc
			}
		}
	}
	return nil
}

func (in *x11Input) fake(typ, detail byte) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	// FakeInput: type 2=KeyPress 3=KeyRelease 4=ButtonPress 5=ButtonRelease 6=MotionNotify
	return xtest.FakeInputChecked(in.conn, typ, detail, 0, in.root, 0, 0, 0).Check()
}

func (in *x11Input) MouseMove(x, y int) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	return xtest.FakeInputChecked(in.conn, 6 /*MotionNotify*/, 0, 0, in.root,
		int16(x), int16(y), 0).Check()
}

func (in *x11Input) MouseButton(button int, down bool) error {
	typ := byte(5)
	if down {
		typ = 4
	}
	return in.fake(typ, byte(button))
}

func (in *x11Input) Wheel(dy int) error {
	b := 5 // 滚下
	if dy < 0 {
		b = 4
	}
	if err := in.MouseButton(b, true); err != nil {
		return err
	}
	return in.MouseButton(b, false)
}

// Key keysym 注入：优先无修饰列，否则 Shift 列（先按下 Shift）
func (in *x11Input) Key(keysym uint32, down bool) error {
	kc, needShift := in.lookup(keysym)
	if kc == 0 {
		return nil // 未知符号：忽略（前端不发送未映射键）
	}
	if needShift {
		shiftKC, ok := in.sym2kc[0xffe1] // XK_Shift_L
		if !ok {
			return nil
		}
		if down {
			if err := in.fake(2, shiftKC); err != nil {
				return err
			}
		}
		defer func() {
			if !down {
				_ = in.fake(3, shiftKC)
			}
		}()
	}
	typ := byte(3)
	if down {
		typ = 2
	}
	return in.fake(typ, kc)
}

func (in *x11Input) lookup(keysym uint32) (kc uint8, needShift bool) {
	if kc, ok := in.sym2kc[keysym]; ok {
		return kc, false
	}
	if kc, ok := in.sym2kcShift[keysym]; ok {
		return kc, true
	}
	return 0, false
}

func (in *x11Input) Close() { in.conn.Close() }
