//go:build !linux && !windows

package clipboard

func open() (Board, bool) { return nil, false }
