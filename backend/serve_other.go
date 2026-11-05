//go:build !windows

package main

import (
	"log"
	"net/http"
)

// serveHTTP 非 Windows 平台：标准 ListenAndServe（REUSEADDR 语义无 Windows 的双实例问题）
func serveHTTP(addr string, h http.Handler) error {
	log.Printf("🐙 灵枢 LinkHub 启动 http://0.0.0.0%s", addr)
	return http.ListenAndServe(addr, h)
}
