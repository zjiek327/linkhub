//go:build linux

package monitor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ttl = 5 * time.Second

type entry struct {
	name string
	at   time.Time
}

type Resolver struct {
	mu      sync.Mutex
	entries map[uint32]entry
}

func NewResolver() *Resolver {
	return &Resolver{entries: make(map[uint32]entry)}
}

func (r *Resolver) Name(pid uint32) string {
	r.mu.Lock()
	e, ok := r.entries[pid]
	r.mu.Unlock()
	if ok && time.Since(e.at) < ttl {
		return e.name
	}

	name := resolve(pid)
	r.mu.Lock()
	r.entries[pid] = entry{name: name, at: time.Now()}
	r.mu.Unlock()
	return name
}

func resolve(pid uint32) string {
	if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil {
		return filepath.Base(exe)
	}
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		s := string(b)
		open := strings.IndexByte(s, '(')
		close := strings.LastIndexByte(s, ')')
		if open >= 0 && close > open {
			return s[open+1 : close]
		}
	}
	return strconv.FormatUint(uint64(pid), 10)
}
