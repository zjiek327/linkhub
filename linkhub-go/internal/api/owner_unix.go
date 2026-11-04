//go:build !windows

package api

import (
	"fmt"
	"syscall"
)

// fileOwner 从 FileInfo.Sys() 提取属主（Unix: uid:gid；Windows 无此概念返回空）
func fileOwner(sys any) string {
	if st, ok := sys.(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", st.Uid, st.Gid)
	}
	return ""
}
