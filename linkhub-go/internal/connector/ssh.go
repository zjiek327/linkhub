package connector

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

func init() { Register("ssh", NewSSH) }

type SSHConnector struct {
	params  map[string]interface{}
	client  *ssh.Client
	session *ssh.Session
	stdin   io.Writer
	stdout  io.Reader
}

func NewSSH(params map[string]interface{}) (Connector, error) {
	return &SSHConnector{params: params}, nil
}

func (c *SSHConnector) Open(ctx context.Context) error {
	host, _ := c.params["host"].(string)
	if host == "" {
		return fmt.Errorf("SSH 未配置 host")
	}
	port := 22
	if p, ok := c.params["port"].(float64); ok {
		port = int(p)
	}
	user, _ := c.params["username"].(string)
	password, _ := c.params["password"].(string)

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // auto 信任（首次记录），严格模式后续加
		Timeout: 10 * time.Second,
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)), config)
	if err != nil {
		return fmt.Errorf("SSH 连接 %s 失败: %w", host, err)
	}
	c.client = client
	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	c.session = sess
	stdin, _ := sess.StdinPipe()
	c.stdin = stdin
	c.stdout, _ = sess.StdoutPipe()
	if err := sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{}); err != nil {
		return err
	}
	return sess.Shell()
}

func (c *SSHConnector) Close() error {
	if c.session != nil {
		c.session.Close()
	}
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

func (c *SSHConnector) Write(data []byte) error {
	if c.session == nil {
		return fmt.Errorf("SSH 未打开")
	}
	_, err := c.stdin.Write(data)
	return err
}

func (c *SSHConnector) Read() (io.Reader, error) {
	if c.stdout == nil {
		return nil, fmt.Errorf("SSH 未打开")
	}
	return c.stdout, nil
}

// Resize SSH PTY 窗口尺寸
func (c *SSHConnector) Resize(rows, cols int) error {
	if c.session == nil {
		return fmt.Errorf("SSH 未连接")
	}
	return c.session.WindowChange(rows, cols)
}
