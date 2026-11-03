package capture

import (
	"bytes"
	"image"
	"image/jpeg"
	"log"
	"sync"
	"time"
)

// ---------- 编码器：BGRA→RGB→JPEG（image/jpeg 要求 *image.RGBA 的 RGB 通道，需转一道） ----------

// encodeBlock 把 BGRA 块编码为 JPEG（quality 内部固定 70，远程桌面够用且带宽低）
func encodeBlock(dst *bytes.Buffer, pix []byte, stride, x, y, w, h int) error {
	// 构小 RGBA：BGRA（小端）→ image.RGBA 期望 R,G,B,A 顺序
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		src := pix[(y+row)*stride+x*4:]
		dstRow := m.Pix[row*m.Stride:]
		for col := 0; col < w; col++ {
			b, g, r, a := src[col*4], src[col*4+1], src[col*4+2], src[col*4+3]
			dstRow[col*4], dstRow[col*4+1], dstRow[col*4+2], dstRow[col*4+3] = r, g, b, a
		}
	}
	return jpeg.Encode(dst, m, &jpeg.Options{Quality: 70})
}

// ---------- 脏矩形引擎 ----------

const blockSize = 64 // 脏块边长（64px：块太小帧头开销大，太大差分浪费）

type block struct {
	x, y, w, h int
	jpeg       []byte
}

// Stream 采集会话：定时抓帧、块级差分、JPEG 编码，帧通过 out 回调发出
type Stream struct {
	mu      sync.Mutex
	scr     Screen
	primary Screen // 切换前的首选源（备用源工作期间保留，用于恢复探测）
	alt     Screen // 当前备用源（nil=未切换）
	last    []byte // 上一帧 BGRA
	buf     []byte // 抓帧缓冲
	fps     int
	closed  bool
	stopCh  chan struct{}
	once    sync.Once
}

// NewStream 创建采集流（fps=0 默认 12）
func NewStream(scr Screen, fps int) *Stream {
	if fps <= 0 || fps > 30 {
		fps = 12
	}
	sz := scr.Size()
	st := &Stream{scr: scr, fps: fps,
		last: make([]byte, sz.X*sz.Y*4), buf: make([]byte, sz.X*sz.Y*4),
		stopCh: make(chan struct{})}
	return st
}

// Run 阻塞采集循环；onFrame 收到完整序列化帧（msg=1 帧格式见 capture.go 头注释）
// 帧错误一律跳过不断流：锁屏/安全桌面/分辨率切换都会让 BitBlt/Mag 瞬时失败，
// 但解锁后采集自动恢复——会话必须活着撑过整个锁屏周期（远程解锁的前提）
func (st *Stream) Run(onFrame func([]byte)) error {
	tick := time.NewTicker(time.Second / time.Duration(st.fps))
	defer tick.Stop()
	fullRedraw := true
	errStreak := 0
	successes := 0
	probeOK := make(chan bool, 1)
	for {
		select {
		case <-st.stopCh:
			return nil
		case <-tick.C:
		case ok := <-probeOK:
			// 备用源工作期间探测到首选源恢复（解锁）：切回
			if ok && st.alt != nil {
				log.Printf("[capture] 首选采集源恢复，切回")
				st.scr = st.primary
				st.alt = nil
				fullRedraw = true
				st.resizeBuf(st.scr)
			}
			continue
		}
		frame, err := st.scr.Frame(st.buf)
		if err != nil {
			errStreak++
			if errStreak == 1 || errStreak%120 == 0 {
				log.Printf("[capture] frame error streak=%d: %v", errStreak, err)
			}
			// 连续 3 帧失败：锁屏/安全桌面场景，切备用源（如 Mag→GDI）
			if errStreak >= 3 && st.alt == nil {
				if alt := AlternateFor(st.scr); alt != nil {
					log.Printf("[capture] 切换备用采集源 %T（首选源连续失败）", alt)
					st.primary = st.scr
					st.alt = alt
					st.scr = alt
					fullRedraw = true
					st.resizeBuf(alt)
					errStreak = 0
				}
			}
			continue
		}
		if errStreak > 0 {
			log.Printf("[capture] frame recovered (streak was %d)", errStreak)
			errStreak = 0
		}
		if st.alt != nil {
			// 备用源每 150 帧成功（≈12s@12fps）异步探测首选源是否恢复；
			// 探测走独立缓冲，Mag 的 3s 超时不阻塞采集循环
			successes++
			if successes%150 == 0 && len(probeOK) == 0 {
				p := st.primary
				go func() {
					sz := p.Size()
					if _, err := p.Frame(make([]byte, sz.X*sz.Y*4)); err == nil {
						probeOK <- true
					}
				}()
			}
		}
		sz := st.scr.Size()
		fw, fh := sz.X, sz.Y
		if len(frame) < fw*fh*4 {
			continue // 尺寸变化等异常帧，跳过
		}

		// 块差分
		blocks := []block{}
		for by := 0; by < fh; by += blockSize {
			for bx := 0; bx < fw; bx += blockSize {
				bw := min(blockSize, fw-bx)
				bh := min(blockSize, fh-by)
				if !fullRedraw && !blockDiffers(st.last, frame, fw*4, bx, by, bw, bh) {
					continue
				}
				var jb bytes.Buffer
				jb.Grow(8 * 1024)
				if err := encodeBlock(&jb, frame, fw*4, bx, by, bw, bh); err != nil {
					continue
				}
				blocks = append(blocks, block{x: bx, y: by, w: bw, h: bh, jpeg: jb.Bytes()})
			}
		}
		copy(st.last, frame)
		if len(blocks) == 0 {
			continue
		}
		out := serializeFrame(fw, fh, blocks)
		fullRedraw = false
		onFrame(out)
	}
}

// serializeFrame 打包：[1][2B w][2B h][2B n] + 每块 [2B x][2B y][2B w][2B h][4B len][jpeg]
func serializeFrame(w, h int, blocks []block) []byte {
	size := 1 + 2 + 2 + 2
	for _, b := range blocks {
		size += 12 + len(b.jpeg)
	}
	out := make([]byte, size)
	out[0] = 1
	put16(out[1:], w)
	put16(out[3:], h)
	put16(out[5:], len(blocks))
	off := 7
	for _, b := range blocks {
		put16(out[off:], b.x)
		put16(out[off+2:], b.y)
		put16(out[off+4:], b.w)
		put16(out[off+6:], b.h)
		put32(out[off+8:], len(b.jpeg))
		off += 12
		copy(out[off:], b.jpeg)
		off += len(b.jpeg)
	}
	return out
}

func put16(b []byte, v int) { b[0] = byte(v); b[1] = byte(v >> 8) }
func put32(b []byte, v int) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

// blockDiffers 比较上一帧与当前帧的指定块（快速路径：4 字节步进抽样）
func blockDiffers(a, b []byte, stride, x, y, w, h int) bool {
	for row := 0; row < h; row++ {
		pa := a[(y+row)*stride+x*4:]
		pb := b[(y+row)*stride+x*4:]
		for col := 0; col < w*4; col += 16 { // 每 4 像素抽 1
			if pa[col] != pb[col] || pa[col+1] != pb[col+1] || pa[col+2] != pb[col+2] {
				return true
			}
		}
	}
	return false
}

// Stop 结束采集循环
func (st *Stream) Stop() {
	st.once.Do(func() {
		close(st.stopCh)
		st.scr.Close()
	})
}

// resizeBuf 按新采集源尺寸重建缓冲（切换 Mag↔GDI 时尺寸可能变化）
func (st *Stream) resizeBuf(s Screen) {
	sz := s.Size()
	if n := sz.X * sz.Y * 4; n > cap(st.buf) || n > cap(st.last) {
		st.buf = make([]byte, n)
		st.last = make([]byte, n)
	}
}
