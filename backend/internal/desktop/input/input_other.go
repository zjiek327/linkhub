//go:build !linux && !windows

package input

func open() (inj Injector, ok bool) { return nil, false }
