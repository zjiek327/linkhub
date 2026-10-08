package connector

import (
	"context"
	"fmt"
	"io"
)

// Connector 是所有设备连接方式的插件接口
type Connector interface {
	Open(ctx context.Context) error
	Close() error
	Write(data []byte) error
	Read() (io.Reader, error) // 返回持续读取流
}

// ConnectorFactory 由参数构造连接器
type ConnectorFactory func(params map[string]interface{}) (Connector, error)

var registry = map[string]ConnectorFactory{}

// Register 注册连接器（init 时调用）
func Register(kind string, f ConnectorFactory) {
	registry[kind] = f
}

// Create 按类型创建连接器
func Create(kind string, params map[string]interface{}) (Connector, error) {
	f, ok := registry[kind]
	if !ok {
		return nil, fmt.Errorf("不支持的连接方式: %s（可用: %v）", kind, Kinds())
	}
	return f(params)
}

// Kinds 返回所有已注册类型
func Kinds() []string {
	ks := make([]string, 0, len(registry))
	for k := range registry {
		ks = append(ks, k)
	}
	return ks
}
