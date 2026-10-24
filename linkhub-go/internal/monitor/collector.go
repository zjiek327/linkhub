//go:build linux

package monitor

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

type Stats struct {
	PID      uint32
	Protocol uint16
	Sent     uint64
	Recv     uint64
}

type Collector struct {
	traffic *ebpf.Map
	progs   []*ebpf.Program
	links   []link.Link
}

func New() (*Collector, error) {
	traffic, err := ebpf.NewMap(&ebpf.MapSpec{
		Name:       "traffic",
		Type:       ebpf.LRUCPUHash,
		KeySize:    8,
		ValueSize:  16,
		MaxEntries: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("create traffic map: %w", err)
	}

	c := &Collector{traffic: traffic}
	for _, ps := range probeSpecs {
		prog, err := loadProbe(ps, traffic.FD())
		if err != nil {
			c.Close()
			return nil, err
		}
		c.progs = append(c.progs, prog)

		l, err := link.Kretprobe(ps.name, prog, nil)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("attach kretprobe %s: %w", ps.name, err)
		}
		c.links = append(c.links, l)
	}
	return c, nil
}

func loadProbe(ps probeSpec, mapFD int) (*ebpf.Program, error) {
	spec := &ebpf.ProgramSpec{
		Name:         ps.name,
		Type:         ebpf.Kprobe,
		License:      "GPL",
		Instructions: probeInstructions(ps, mapFD),
	}
	prog, err := ebpf.NewProgram(spec)
	if err != nil {
		var verr *ebpf.VerifierError
		if errors.As(err, &verr) {
			return nil, fmt.Errorf("load program %s: %w\nverifier log:\n%s", ps.name, err, verr.Log)
		}
		return nil, fmt.Errorf("load program %s: %w", ps.name, err)
	}
	return prog, nil
}

func (c *Collector) Close() {
	for _, l := range c.links {
		l.Close()
	}
	for _, p := range c.progs {
		p.Close()
	}
	if c.traffic != nil {
		c.traffic.Close()
	}
}

func (c *Collector) Sample() ([]Stats, error) {
	var out []Stats
	var key [8]byte
	values := make([][2]uint64, ebpf.MustPossibleCPU())

	it := c.traffic.Iterate()
	for it.Next(&key, &values) {
		s := Stats{
			PID:      binary.LittleEndian.Uint32(key[0:4]),
			Protocol: binary.LittleEndian.Uint16(key[4:6]),
		}
		for _, v := range values {
			s.Sent += v[0]
			s.Recv += v[1]
		}
		out = append(out, s)
	}
	if err := it.Err(); err != nil && !errors.Is(err, ebpf.ErrIterationAborted) {
		return nil, err
	}
	return out, nil
}
