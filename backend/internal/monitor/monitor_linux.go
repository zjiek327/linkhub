//go:build linux

package monitor

import (
	"sort"
	"time"
)

// Service Linux 实现：eBPF collector + 采样循环
type Service struct {
	serviceCore
	collector *Collector
	resolver  *Resolver
	prev      map[bucketKey]Stats // 上次累计值
}

// Start 启动监控（eBPF 不可用返回 disabled 服务而非错误——节点仍可跑其他功能）
func Start() *Service {
	s := &Service{resolver: NewResolver(), prev: map[bucketKey]Stats{}}
	s.stop = make(chan struct{})
	s.totals = map[bucketKey]ProcFlow{}
	c, err := New()
	if err != nil {
		s.err = "流量采集不可用（需 root/CAP_BPF 运行: " + err.Error() + "）"
		return s
	}
	s.collector = c
	s.enabled = true
	go s.loop()
	return s
}

func (s *Service) loop() {
	tick := time.NewTicker(sampleInterval)
	defer tick.Stop()
	// 初始基线
	if stats, err := s.collector.Sample(); err == nil {
		for _, st := range stats {
			s.prev[bucketKey{st.PID, st.Protocol}] = st
		}
	}
	for {
		select {
		case <-s.stop:
			return
		case now := <-tick.C:
			s.sampleOnce(now)
		}
	}
}

func (s *Service) sampleOnce(now time.Time) {
	stats, err := s.collector.Sample()
	if err != nil {
		return
	}
	cur := map[bucketKey]Stats{}
	for _, st := range stats {
		cur[bucketKey{st.PID, st.Protocol}] = st
	}
	rates := []ProcRate{}
	for k, st := range cur {
		p, ok := s.prev[k]
		var dSent, dRecv uint64
		if ok {
			if st.Sent > p.Sent {
				dSent = st.Sent - p.Sent
			}
			if st.Recv > p.Recv {
				dRecv = st.Recv - p.Recv
			}
		}
		// 增量守卫：1s 内物理上界 10GB（万兆线速≈1.25GB/s）；超出视为
		// eBPF map 键被 LRU 逐出后重建等异常，丢弃本帧增量
		if dSent > 10<<30 || dRecv > 10<<30 {
			dSent, dRecv = 0, 0
		}
		t := s.totals[k]
		t.PID, t.Proto = k.pid, k.proto
		t.Name = s.resolver.Name(k.pid)
		t.Sent += dSent
		t.Recv += dRecv
		s.totals[k] = t
		if dSent > 0 || dRecv > 0 {
			rates = append(rates, ProcRate{PID: k.pid, Name: t.Name, Proto: k.proto,
				Rate: SentRecv{Sent: dSent, Recv: dRecv}})
		}
		s.aggregate(now, dSent, dRecv)
	}
	s.prev = cur
	sort.Slice(rates, func(i, j int) bool {
		a := rates[i].Rate.Sent + rates[i].Rate.Recv
		b := rates[j].Rate.Sent + rates[j].Rate.Recv
		return a > b
	})
	// 秒桶（曲线主数据源）：本秒总增量
	secSent, secRecv := uint64(0), uint64(0)
	for _, r := range rates {
		secSent += r.Rate.Sent
		secRecv += r.Rate.Recv
	}
	s.mu.Lock()
	s.seconds = append(s.seconds, Bucket{Start: now.Truncate(time.Second), Total: SentRecv{secSent, secRecv}})
	if len(s.seconds) > secondBuckets {
		s.seconds = s.seconds[len(s.seconds)-secondBuckets:]
	}
	s.perSec = rates
	s.mu.Unlock()
}

// aggregate 分钟桶聚合（含每分钟滚动）
func (s *Service) aggregate(now time.Time, sent, recv uint64) {
	minStart := now.Truncate(time.Minute)
	if s.curMin == nil || !s.curMin.Start.Equal(minStart) {
		if s.curMin != nil {
			s.minutes = append(s.minutes, *s.curMin)
			if len(s.minutes) > historyBuckets {
				s.minutes = s.minutes[len(s.minutes)-historyBuckets:]
			}
		}
		s.curMin = &Bucket{Start: minStart}
	}
	s.curMin.Total.Sent += sent
	s.curMin.Total.Recv += recv
}

// Close 停止采集
func (s *Service) Close() {
	s.once.Do(func() {
		if s.stop != nil {
			close(s.stop)
		}
		if s.collector != nil {
			s.collector.Close()
		}
	})
}
