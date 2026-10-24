//go:build !linux

// 非 Linux 平台：监控服务空实现（eBPF 仅 Linux）
package monitor

import "time"

type Service struct{ serviceCore }

func Start() *Service {
	s := &Service{}
	s.err = "流量监控仅支持 Linux 节点（eBPF）"
	return s
}

func (s *Service) Close() {}

var _ = time.Second
