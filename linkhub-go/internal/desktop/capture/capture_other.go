//go:build !linux && !windows

package capture

func open() (scr Screen, ok bool) { return nil, false }

func alternateFor(s Screen) Screen { return nil }
