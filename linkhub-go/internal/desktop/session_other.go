//go:build !linux && !windows

package desktop

// graphicalSession 其他平台暂不细分，按无桌面处理
func graphicalSession() (bool, string) {
	return false, ""
}
