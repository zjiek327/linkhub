package monitor

import (
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/shirou/gopsutil/v4/sensors"
)

// ---------- 指标数据结构（快照 + 环形历史） ----------

type MetricsSnapshot struct {
	TS         time.Time        `json:"ts"`
	Host       HostInfo         `json:"host"`
	CPU        CPUMetrics       `json:"cpu"`
	Load       LoadMetrics      `json:"load"`
	Mem        MemMetrics       `json:"mem"`
	Disks      []DiskMetrics    `json:"disks"`
	Net        []NetIOMetrics   `json:"net"`
	NetRate    NetTotalRate     `json:"net_rate"`
	Procs      []ProcMetrics    `json:"procs"`      // CPU/RSS Top N
	NetStat    TCPStatMetrics   `json:"net_stat"`   // TCP 连接状态分布
	Temps      []TempMetrics    `json:"temps"`      // 有则返回
	Self       SelfMetrics      `json:"self"`       // LinkHub 进程自身
}

type HostInfo struct {
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`        // linux / windows
	Platform   string `json:"platform"`  // Ubuntu 24.04 / Microsoft Windows 11
	Kernel     string `json:"kernel"`
	Uptime     uint64 `json:"uptime_s"`
	BootTime   int64  `json:"boot_ts"`
}

type CPUMetrics struct {
	Percent  float64   `json:"percent"`    // 总体（采样周期差分）
	PerCore  []float64 `json:"per_core"`   // 每核
	Counts   int       `json:"counts"`     // 逻辑核数
	Model    string    `json:"model"`
}

type LoadMetrics struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	// Windows 无 loadavg：用处理器队列长度近似（>0 即有等待）
	Runners int `json:"runners"`
}

type MemMetrics struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	UsedPct   float64 `json:"used_pct"`
	SwapTotal uint64  `json:"swap_total"`
	SwapUsed  uint64  `json:"swap_used"`
}

type DiskMetrics struct {
	Path      string  `json:"path"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	UsedPct   float64 `json:"used_pct"`
	InodesPct float64 `json:"inodes_pct"` // inode 用量（Windows 0）
}

type NetIOMetrics struct {
	Name       string `json:"name"`
	SentRate   float64 `json:"sent_rate"`   // B/s（差分）
	RecvRate   float64 `json:"recv_rate"`
	SentTotal  uint64  `json:"sent_total"`
	RecvTotal  uint64  `json:"recv_total"`
}

type NetTotalRate struct {
	Sent float64 `json:"sent"` // 全网卡 B/s
	Recv float64 `json:"recv"`
}

type ProcMetrics struct {
	PID     int32   `json:"pid"`
	Name    string  `json:"name"`
	CPU     float64 `json:"cpu"` // 单核百分比（>100=多核）
	Mem     float64 `json:"mem"` // RSS MB
	User    string  `json:"user"`
}

type TCPStatMetrics struct {
	Established int `json:"established"`
	TimeWait    int `json:"time_wait"`
	CloseWait   int `json:"close_wait"`
	Listen      int `json:"listen"`
	Other       int `json:"other"`
}

type TempMetrics struct {
	Sensor string  `json:"sensor"`
	C      float64 `json:"c"`
}

type SelfMetrics struct {
	GoRoutines int    `json:"go_routines"`
	HeapMB     uint64 `json:"heap_mb"`
	RSSMB      uint64 `json:"rss_mb"`
	NumGC      uint32 `json:"num_gc"`
	Sessions   int    `json:"sessions"`    // LinkHub 在线会话数（注入）
}

// ---------- 环形历史（复用秒/分钟桶思路，指标用 15s 采样 × 240 = 1h；分钟桶 × 48 = 48h） ----------

type metricsPoint struct {
	t        time.Time
	cpuPct   float64
	memUsed  uint64
	netSent  float64 // B/s
	netRecv  float64
}

// MetricsService 系统指标采集（全平台，gopsutil）
type MetricsService struct {
	mu        sync.RWMutex
	snap      MetricsSnapshot
	hist      []metricsPoint // 15s ×240 = 1h
	minuteAgg []metricsPoint // 分钟均值 ×48 = 48h
	stop      chan struct{}
	once      sync.Once
	lastNet   map[string]net.IOCountersStat // 上次网卡计数（差分基线）
	lastAt    time.Time
	sessions  func() int // 会话数注入（main 里接 session manager）
}

// StartMetrics 启动系统指标采集（无 root 要求，失败不致命：快照尽量填）
func StartMetrics(sessions func() int) *MetricsService {
	m := &MetricsService{stop: make(chan struct{}), sessions: sessions, lastNet: map[string]net.IOCountersStat{}}
	m.sample() // 首采样建立基线
	go m.loop()
	return m
}

func (m *MetricsService) loop() {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-tick.C:
			m.sample()
		}
	}
}

func (m *MetricsService) sample() {
	now := time.Now()
	s := MetricsSnapshot{TS: now, Disks: []DiskMetrics{}, Net: []NetIOMetrics{}, Procs: []ProcMetrics{}, Temps: []TempMetrics{}}

	// 主机信息（低频数据，每次都取也不贵——gopsutil 内部无缓存但很快）
	if hi, err := host.Info(); err == nil {
		s.Host = HostInfo{Hostname: hi.Hostname, OS: hi.OS, Platform: hi.Platform + " " + hi.PlatformVersion,
			Kernel: hi.KernelVersion, Uptime: hi.Uptime, BootTime: int64(hi.BootTime)}
	}
	if cts, err := cpu.Counts(true); err == nil {
		s.CPU.Counts = cts
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		s.CPU.Model = infos[0].ModelName
	}
	// CPU 差分（interval=0 非阻塞，内部对比上次调用）
	if pct, err := cpu.Percent(0, false); err == nil && len(pct) > 0 {
		s.CPU.Percent = pct[0]
	}
	if cores, err := cpu.Percent(0, true); err == nil {
		s.CPU.PerCore = cores
	}
	// 负载
	if lv, err := load.Avg(); err == nil {
		s.Load.Load1, s.Load.Load5, s.Load.Load15 = lv.Load1, lv.Load5, lv.Load15
	}
	if lv, err := load.Misc(); err == nil {
		s.Load.Runners = lv.ProcsRunning
	}
	// 内存
	if mv, err := mem.VirtualMemory(); err == nil {
		s.Mem.Total, s.Mem.Used, s.Mem.Available, s.Mem.UsedPct = mv.Total, mv.Used, mv.Available, mv.UsedPercent
	}
	if sv, err := mem.SwapMemory(); err == nil {
		s.Mem.SwapTotal, s.Mem.SwapUsed = sv.Total, sv.Used
	}
	// 磁盘（物理挂载点）
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			du, err := disk.Usage(p.Mountpoint)
			if err != nil || du.Total == 0 {
				continue
			}
			s.Disks = append(s.Disks, DiskMetrics{Path: p.Mountpoint, Total: du.Total, Used: du.Used, UsedPct: du.UsedPercent})
		}
	}
	// 网卡（差分速率）
	if io, err := net.IOCounters(true); err == nil {
		dt := now.Sub(m.lastAt).Seconds()
		var totalSent, totalRecv float64
		for _, nic := range io {
			if nic.BytesSent == 0 && nic.BytesRecv == 0 {
				continue // 虚拟/无效口
			}
			var sr, rr float64
			if prev, ok := m.lastNet[nic.Name]; ok && dt > 0 {
				if nic.BytesSent >= prev.BytesSent {
					sr = float64(nic.BytesSent-prev.BytesSent) / dt
				}
				if nic.BytesRecv >= prev.BytesRecv {
					rr = float64(nic.BytesRecv-prev.BytesRecv) / dt
				}
			}
			s.Net = append(s.Net, NetIOMetrics{Name: nic.Name, SentRate: sr, RecvRate: rr,
				SentTotal: nic.BytesSent, RecvTotal: nic.BytesRecv})
			totalSent += sr
			totalRecv += rr
		}
		s.NetRate = NetTotalRate{Sent: totalSent, Recv: totalRecv}
		if dt > 0 {
			m.lastNet = map[string]net.IOCountersStat{}
			for _, nic := range io {
				m.lastNet[nic.Name] = nic
			}
			m.lastAt = now
		}
	}
	// 进程 Top（CPU 优先，兼顾 RSS 大户）
	if procs, err := process.Processes(); err == nil {
		type row struct{ p ProcMetrics }
		rows := []ProcMetrics{}
		for _, p := range procs {
			n, _ := p.Name()
			cpuPct, _ := p.CPUPercent()
			rss, _ := p.MemoryInfo()
			u := ""
			if usr, err := p.Username(); err == nil {
				u = usr
			}
			pm := ProcMetrics{PID: p.Pid, Name: n, CPU: cpuPct, User: u}
			if rss != nil {
				pm.Mem = float64(rss.RSS) / 1024 / 1024
			}
			rows = append(rows, pm)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].CPU != rows[j].CPU {
				return rows[i].CPU > rows[j].CPU
			}
			return rows[i].Mem > rows[j].Mem
		})
		if len(rows) > 15 {
			rows = rows[:15]
		}
		s.Procs = rows
	}
	// TCP 连接状态
	if conns, err := net.Connections("tcp"); err == nil {
		for _, c := range conns {
			switch c.Status {
			case "ESTABLISHED":
				s.NetStat.Established++
			case "TIME_WAIT":
				s.NetStat.TimeWait++
			case "CLOSE_WAIT":
				s.NetStat.CloseWait++
			case "LISTEN":
				s.NetStat.Listen++
			default:
				s.NetStat.Other++
			}
		}
	}
	// 温度（有则显示）
	if temps, err := sensors.SensorsTemperatures(); err == nil {
		for _, t := range temps {
			if t.Temperature > 0 {
				s.Temps = append(s.Temps, TempMetrics{Sensor: t.SensorKey, C: t.Temperature})
			}
		}
	}
	// LinkHub 自身
	var rms runtime.MemStats
	runtime.ReadMemStats(&rms)
	s.Self.GoRoutines = runtime.NumGoroutine()
	s.Self.HeapMB = rms.HeapAlloc / 1024 / 1024
	s.Self.NumGC = rms.NumGC
	if p, err := process.NewProcess(int32(os.Getpid())); err == nil {
		if rss, err := p.MemoryInfo(); err == nil && rss != nil {
			s.Self.RSSMB = rss.RSS / 1024 / 1024
		}
	}
	if m.sessions != nil {
		s.Self.Sessions = m.sessions()
	}

	// 历史点（15s 粒度）
	m.mu.Lock()
	m.snap = s
	m.hist = append(m.hist, metricsPoint{t: now, cpuPct: s.CPU.Percent, memUsed: s.Mem.Used,
		netSent: s.NetRate.Sent, netRecv: s.NetRate.Recv})
	if len(m.hist) > 240 {
		m.hist = m.hist[len(m.hist)-240:]
	}
	// 分钟均值聚合
	mm := now.Truncate(time.Minute)
	if len(m.minuteAgg) > 0 && !m.minuteAgg[len(m.minuteAgg)-1].t.Equal(mm) {
		if len(m.minuteAgg) > 48 {
			m.minuteAgg = m.minuteAgg[len(m.minuteAgg)-48:]
		}
		m.minuteAgg = append(m.minuteAgg, metricsPoint{t: mm, cpuPct: s.CPU.Percent, memUsed: s.Mem.Used,
			netSent: s.NetRate.Sent, netRecv: s.NetRate.Recv})
	} else if len(m.minuteAgg) > 0 {
		// 当前分钟滚动更新（简单取最新值代表）
		last := &m.minuteAgg[len(m.minuteAgg)-1]
		last.cpuPct, last.memUsed, last.netSent, last.netRecv = s.CPU.Percent, s.Mem.Used, s.NetRate.Sent, s.NetRate.Recv
	} else {
		m.minuteAgg = append(m.minuteAgg, metricsPoint{t: mm, cpuPct: s.CPU.Percent, memUsed: s.Mem.Used,
			netSent: s.NetRate.Sent, netRecv: s.NetRate.Recv})
	}
	m.mu.Unlock()
}

// Metrics 当前快照
func (m *MetricsService) Metrics() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap
}

// MetricsPoint 导出用历史点
type MetricsPoint struct {
	T       time.Time `json:"t"`
	CPU     float64   `json:"cpu"`
	MemUsed uint64    `json:"mem_used"`
	NetSent float64   `json:"net_sent"`
	NetRecv float64   `json:"net_recv"`
}

// MetricsHistory 曲线历史（≤1h 用 15s 点，更长用分钟点）
func (m *MetricsService) MetricsHistory(window time.Duration) []MetricsPoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	src := m.hist
	if window > time.Hour {
		src = m.minuteAgg
	}
	out := make([]MetricsPoint, 0, len(src))
	for _, p := range src {
		out = append(out, MetricsPoint{T: p.t, CPU: p.cpuPct, MemUsed: p.memUsed, NetSent: p.netSent, NetRecv: p.netRecv})
	}
	return out
}

// Close 停止
func (m *MetricsService) Close() {
	m.once.Do(func() { close(m.stop) })
}
