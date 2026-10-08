package connector

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

func init() { Register("telnet", NewTelnet) }

// Telnet 用原生 TCP 实现（telnet 协议本身很简单：数据流 + IAC 协商跳过）
type TelnetConnector struct {
	params map[string]interface{}
	conn   net.Conn
}

func NewTelnet(params map[string]interface{}) (Connector, error) {
	return &TelnetConnector{params: params}, nil
}

func (c *TelnetConnector) Open(ctx context.Context) error {
	host, _ := c.params["host"].(string)
	if host == "" {
		return fmt.Errorf("Telnet 未配置 host")
	}
	port := 23
	if p, ok := c.params["port"].(float64); ok {
		port = int(p)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 10*time.Second)
	if err != nil {
		return fmt.Errorf("Telnet 连接失败: %w", err)
	}
	c.conn = conn
	// 跳过 IAC 协商（读掉前几百毫秒的协议字节）
	c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 256)
	for {
		_, err := c.conn.Read(buf)
		if err != nil {
			break
		}
	}
	c.conn.SetReadDeadline(time.Time{})
	return nil
}

func (c *TelnetConnector) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *TelnetConnector) Write(data []byte) error {
	if c.conn == nil {
		return fmt.Errorf("Telnet 未打开")
	}
	_, err := c.conn.Write(data)
	return err
}

func (c *TelnetConnector) Read() (io.Reader, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("Telnet 未打开")
	}
	return c.conn, nil
}
