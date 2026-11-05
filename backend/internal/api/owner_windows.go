//go:build windows

package api

// fileOwner 从 FileInfo.Sys() 提取属主：Windows 无 uid/gid 概念，恒空
func fileOwner(sys any) string { return "" }
