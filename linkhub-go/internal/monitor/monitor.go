// Package monitor 节点流量监控（源自 streamflow 项目，纯 Go 内联 eBPF kretprobe 采集）。
// 需要 root/CAP_BPF 运行；不可用时 Enabled=false，API 返回明确错误而非崩溃。
// 平台拆分：Service 及采集实现见 monitor_linux.go；非 Linux 空实现见 monitor_stub.go。
package monitor

import (
	"sort"
	"sync"
	"time"
)

const (
	sampleInterval = time.Second
	secondBuckets  = 3600 // 秒桶 ×3600 = 1h 秒级历史
	historyBuckets = 120  // 分钟桶 ×120 = 2h 分钟级历史（长窗口用）
)

// ProcFlow 单进程单协议的速率快照
type ProcFlow struct {
	PID   uint32 `json:"pid"`
	Name  string `json:"name"`
	Proto uint16 `json:"proto"` // 6=TCP 17=UDP
	Sent  uint64 `json:"sent"`  // 累计字节（本次会话）
	Recv  uint64 `json:"recv"`
}

// Snapshot 实时快照
type Snapshot struct {
	TS     time.Time  `json:"ts"`
	Flows  []ProcFlow `json:"flows"`
	Total  SentRecv   `json:"total"`   // 全进程会话累计
	PerSec []ProcRate `json:"per_sec"` // 每进程当前速率（最近 1s 增量）
}

// SentRecv 上下行
type SentRecv struct {
	Sent uint64 `json:"sent"`
	Recv uint64 `json:"recv"`
}

// ProcRate 进程速率
type ProcRate struct {
	PID   uint32  `json:"pid"`
	Name  string  `json:"name"`
	Proto uint16  `json:"proto"`
	Rate  SentRecv `json:"rate"` // B/s
}

// Bucket 聚合桶
type Bucket struct {
	Start time.Time `json:"start"`
	Total SentRecv  `json:"total"`
}

type bucketKey struct {
	pid   uint32
	proto uint16
}

// serviceCore 平台无关的核心状态（linux 实现与 stub 共用类型）
type serviceCore struct {
	mu        sync.RWMutex
	enabled   bool
	err       string // 不可用原因
	perSec    []ProcRate             // 最近 1s 速率
	totals    map[bucketKey]ProcFlow // 会话累计
	seconds   []Bucket               // 环形秒桶（1h，秒粒度曲线用）
	minutes   []Bucket               // 环形分钟桶（2h，长窗口用）
	curMin    *Bucket                // 当前分钟聚合中
	stop      chan struct{}
	once      sync.Once
}

// Snapshot 当前快照（速率 Top + 会话累计）
func (s *serviceCore) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return Snapshot{TS: time.Now()}
	}
	flows := make([]ProcFlow, 0, len(s.totals))
	var total SentRecv
	for _, t := range s.totals {
		if t.Sent == 0 && t.Recv == 0 {
			continue
		}
		flows = append(flows, t)
		total.Sent += t.Sent
		total.Recv += t.Recv
	}
	sort.Slice(flows, func(i, j int) bool {
		a := flows[i].Sent + flows[i].Recv
		b := flows[j].Sent + flows[j].Recv
		return a > b
	})
	return Snapshot{TS: time.Now(), Flows: flows, Total: total, PerSec: s.perSec}
}

// History 历史曲线数据：≤1h 用秒桶，更长窗口用分钟桶
func (s *serviceCore) History(window time.Duration) []Bucket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var src []Bucket
	if window <= time.Hour && len(s.seconds) > 0 {
		src = s.seconds
	} else {
		src = s.minutes
		if s.curMin != nil {
			src = append(append([]Bucket{}, s.minutes...), *s.curMin)
		}
	}
	cutoff := time.Now().Add(-window)
	out := []Bucket{}
	for _, b := range src {
		if !b.Start.Before(cutoff) {
			out = append(out, b)
		}
	}
	return out
}

// Enabled / Reason
func (s *serviceCore) Enabled() bool  { return s.enabled }
func (s *serviceCore) Reason() string { return s.err }
