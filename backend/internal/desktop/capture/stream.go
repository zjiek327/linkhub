package capture

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// ---------- 编码器：BGRA→RGB→JPEG ----------

// rgbaScratchPool 64x64 块复用缓冲（块边缘小于 64 时按 Rect 裁剪，Pix 复用整块 backing）
var rgbaScratchPool = sync.Pool{New: func() any {
	return image.NewRGBA(image.Rect(0, 0, blockSize, blockSize))
}}

// encodeBlock 把 BGRA 块编码为 JPEG。BGRA→RGBA 用 uint32 字级位运算换序
// （每像素 3 次位运算，比逐字节搬移快 5-8 倍）；quality 由调用方按运动自适应
func encodeBlock(dst *bytes.Buffer, pix []byte, stride, x, y, w, h, quality int) error {
	m := rgbaScratchPool.Get().(*image.RGBA)
	defer rgbaScratchPool.Put(m)
	m.Rect = image.Rect(0, 0, w, h)
	m.Stride = blockSize * 4
	for row := 0; row < h; row++ {
		src := pix[(y+row)*stride+x*4:]
		dstRow := m.Pix[row*m.Stride:]
		for col := 0; col < w; col++ {
			v := binary.LittleEndian.Uint32(src[col*4:])
			// BGRA→RGBA：保留 A/G，交换 R/B 两字节
			v = (v & 0xff00ff00) | ((v & 0x00ff0000) >> 16) | ((v & 0x000000ff) << 16)
			binary.LittleEndian.PutUint32(dstRow[col*4:], v)
		}
	}
	return jpeg.Encode(dst, m, &jpeg.Options{Quality: quality})
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
	last    []byte // 上一帧 BGRA（与 sbuf 同尺寸=可能已降采样）
	buf     []byte // 抓帧缓冲（全分辨率）
	sbuf    []byte // 降采样输出缓冲（step=1 时未用）
	step    int    // 降采样步长（1=全分辨率，2=1/2，3=1/3，4=1/4）
	fps     int
	closed  bool
	stopCh  chan struct{}
	once    sync.Once
	// errHook 帧错误回调（streak=连续失败次数, onAlt=是否已在备用源上；
	// streak=1 及每 60 次触发，供上层做"采集持续失败"用户通知）
	errHook func(streak int, onAlt bool, err error)
	perfSink func(PerfSample)
}

// OnError 注册帧错误回调（nil 清除）
func (st *Stream) OnError(h func(streak int, onAlt bool, err error)) { st.errHook = h }

// PerfSample 采集管线分段耗时采样（每 perfEvery 帧平均一次，诊断延迟用）
type PerfSample struct {
	Frames          int
	Src             string // 采集源类型（*capture.magScreen / *capture.gdiScreen）
	AvgCapture      time.Duration // Frame() 抓屏
	AvgDiff         time.Duration // 差分扫描
	AvgEncode       time.Duration // 并行 JPEG 编码
	AvgSerialize    time.Duration // 帧打包
	AvgDirty        int           // 平均脏块数
	AvgPayloadBytes int           // 平均帧字节
}

// OnPerf 注册分段耗时上报回调（nil 关闭）
func (st *Stream) OnPerf(h func(PerfSample)) { st.perfSink = h }

// NewStream 创建采集流（fps<=0 用默认 24，可用 LINKHUB_DESKTOP_FPS 覆盖，上限 60）
// LINKHUB_DESKTOP_SCALE 降分辨率采集：0.5/0.33/0.25 或 2/3/4（=步长），
// 像素量降为 1/n²——抓屏/差分/编码/字节全链降耗，2K/4K 屏下 cap 可砍半以上；
// 浏览器 CSS 拉伸显示，输入坐标由上层按 Scale() 放大回全分辨率
func NewStream(scr Screen, fps int) *Stream {
	if fps <= 0 {
		fps = 24
		if v := os.Getenv("LINKHUB_DESKTOP_FPS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				fps = n
			}
		}
	}
	if fps > 60 {
		fps = 60
	}
	step := 1
	if v := os.Getenv("LINKHUB_DESKTOP_SCALE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			if f < 1 {
				step = int(1/f + 0.5) // 0.5→2、0.33→3、0.25→4
			} else {
				step = int(f + 0.5) // 2→1/2、4→1/4
			}
			if step < 1 {
				step = 1
			}
			if step > 4 {
				step = 4
			}
		}
	}
	if step > 1 {
		log.Printf("[capture] 降分辨率采集: 1/%d（LINKHUB_DESKTOP_SCALE=%s）", step, os.Getenv("LINKHUB_DESKTOP_SCALE"))
	}
	sz := scr.Size()
	st := &Stream{scr: scr, fps: fps, step: step,
		last: make([]byte, sz.X*sz.Y*4), buf: make([]byte, sz.X*sz.Y*4),
		stopCh: make(chan struct{})}
	return st
}

// Scale 返回降采样步长（1=全分辨率）。上层注入输入坐标时须乘回步长
func (st *Stream) Scale() int { return st.step }

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
	// E 方案分段计时：每 perfEvery 帧汇总平均一次（仅成功出帧计入）
	const perfEvery = 48 // ≈2s@24fps
	var pCapture, pDiff, pEncode, pSerialize time.Duration
	var pDirty, pSize, pFrames int
	report := func() {
		if st.perfSink == nil || pFrames == 0 {
			pCapture, pDiff, pEncode, pSerialize, pDirty, pSize, pFrames = 0, 0, 0, 0, 0, 0, 0
			return
		}
		n := time.Duration(pFrames)
		src := fmt.Sprintf("%T", st.scr)
		if st.step > 1 {
			src += fmt.Sprintf(" 1/%d", st.step)
		}
		st.perfSink(PerfSample{Frames: pFrames, Src: src,
			AvgCapture: pCapture / n, AvgDiff: pDiff / n, AvgEncode: pEncode / n, AvgSerialize: pSerialize / n,
			AvgDirty: pDirty / pFrames, AvgPayloadBytes: pSize / pFrames})
		pCapture, pDiff, pEncode, pSerialize, pDirty, pSize, pFrames = 0, 0, 0, 0, 0, 0, 0
	}
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
		tc := time.Now()
		frame, err := st.scr.Frame(st.buf)
		if err != nil {
			errStreak++
			if errStreak == 1 || errStreak%120 == 0 {
				log.Printf("[capture] frame error streak=%d: %v", errStreak, err)
			}
			if st.errHook != nil && (errStreak == 1 || errStreak%60 == 0) {
				st.errHook(errStreak, st.alt != nil, err)
			}
			// 连续 2 帧失败：锁屏/安全桌面场景，切备用源（如 Mag→GDI）。
			// Mag 单帧超时 3s，2 帧≈6s 内完成切换（3 帧要 ~9s，用户早已判定卡死）
			if errStreak >= 2 && st.alt == nil {
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
		if st.step > 1 {
			// 降采样：cap 计时含缩放（目标是"整帧就绪"耗时）。盒式平均保文字可读性
			sw, sh := fw/st.step, fh/st.step
			if cap(st.sbuf) < sw*sh*4 {
				st.sbuf = make([]byte, sw*sh*4)
			}
			downscale(frame, st.sbuf, fw, fh, st.step)
			frame = st.sbuf[:sw*sh*4]
			fw, fh = sw, sh
		}
		pCapture += time.Since(tc)
		td := time.Now()

		// 块差分：先收集脏块，再并行编码（块间无依赖，按索引保序；
		// 多核并行把编码耗时降为 1/N，是高 fps 下的关键路径）
		type dirtyBlock struct{ x, y, w, h int }
		var dirty []dirtyBlock
		for by := 0; by < fh; by += blockSize {
			for bx := 0; bx < fw; bx += blockSize {
				bw := min(blockSize, fw-bx)
				bh := min(blockSize, fh-by)
				if !fullRedraw && !blockDiffers(st.last, frame, fw*4, bx, by, bw, bh) {
					continue
				}
				dirty = append(dirty, dirtyBlock{bx, by, bw, bh})
			}
		}
		copy(st.last, frame)
		if len(dirty) == 0 {
			continue
		}
		pDiff += time.Since(td)
		te := time.Now()
		// 自适应质量：脏块超 1/6（运动帧）降 q40 减字节→降 WS 写出与网络耗时；
		// 静止/小变动 q60 保清晰度
		quality := 60
		if len(dirty) > (fw/blockSize)*(fh/blockSize)/6 {
			quality = 40
		}
		jpegs := make([][]byte, len(dirty))
		var encWG sync.WaitGroup
		sem := make(chan struct{}, runtime.GOMAXPROCS(0))
		for i, d := range dirty {
			encWG.Add(1)
			go func(i int, d dirtyBlock) {
				defer encWG.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				var jb bytes.Buffer
				jb.Grow(8 * 1024)
				if err := encodeBlock(&jb, frame, fw*4, d.x, d.y, d.w, d.h, quality); err == nil {
					jpegs[i] = jb.Bytes()
				}
			}(i, d)
		}
		encWG.Wait()
		pEncode += time.Since(te)
		ts := time.Now()
		blocks := make([]block, 0, len(dirty))
		for i, d := range dirty {
			if jpegs[i] != nil {
				blocks = append(blocks, block{x: d.x, y: d.y, w: d.w, h: d.h, jpeg: jpegs[i]})
			}
		}
		out := serializeFrame(fw, fh, blocks)
		fullRedraw = false
		onFrame(out)
		// E 方案：onFrame 之后统计（写出耗时在回调内累计），凑满窗口上报
		pSerialize += time.Since(ts)
		pDirty += len(dirty)
		pSize += len(out)
		pFrames++
		if pFrames >= perfEvery {
			report()
		}
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

// downscale n×n 盒式平均降采样（src 全分辨率 BGRA → dst sw×sh）。边缘余数像素丢弃
func downscale(src, dst []byte, fw, fh, n int) {
	sw, sh := fw/n, fh/n
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			var r, g, b, a uint32
			for dy := 0; dy < n; dy++ {
				row := src[((y*n+dy)*fw+x*n)*4:]
				for dx := 0; dx < n; dx++ {
					p := row[dx*4:]
					b += uint32(p[0])
					g += uint32(p[1])
					r += uint32(p[2])
					a += uint32(p[3])
				}
			}
			m := uint32(n * n)
			o := (y*sw + x) * 4
			dst[o] = byte(b / m)
			dst[o+1] = byte(g / m)
			dst[o+2] = byte(r / m)
			dst[o+3] = byte(a / m)
		}
	}
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
