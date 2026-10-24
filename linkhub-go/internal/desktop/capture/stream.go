package capture

import (
	"bytes"
	"image"
	"image/jpeg"
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
	mu     sync.Mutex
	scr    Screen
	last   []byte // 上一帧 BGRA
	buf    []byte // 抓帧缓冲
	fps    int
	closed bool
	stopCh chan struct{}
	once   sync.Once
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
func (st *Stream) Run(onFrame func([]byte)) error {
	tick := time.NewTicker(time.Second / time.Duration(st.fps))
	defer tick.Stop()
	fullRedraw := true
	for {
		select {
		case <-st.stopCh:
			return nil
		case <-tick.C:
		}
		frame, err := st.scr.Frame(st.buf)
		if err != nil {
			return err
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

func put16(b []byte, v int)      { b[0] = byte(v); b[1] = byte(v >> 8) }
func put32(b []byte, v int)      { b[0] = byte(v); b[1] = byte(v >> 8); b[2] = byte(v >> 16); b[3] = byte(v >> 24) }

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
