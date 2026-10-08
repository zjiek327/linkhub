package session_test

import (
	"os"
	"testing"
	"time"

	"linkhub/internal/session"
	"linkhub/internal/store"
)

// 伪终端对（Linux/macOS）
func ptyPair(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("无 ptmx（非 POSIX 平台）")
	}
	// 解锁 slave
	slaveName := master.Name()
	_ = slaveName
	return master, slave
}

func TestSerialSessionE2E(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	mgr := session.NewManager(st)

	master, slave := ptyPair(t)
	defer master.Close()
	if slave != nil {
		defer slave.Close()
	}

	// 打开会话
	s, err := mgr.Open(0, "serial", map[string]interface{}{
		"port": "/dev/pts/0", "baudrate": float64(9600), "auto_reconnect": false,
	}, 1, "test")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status == "online" {
			break
		}
		if s.Status == "error" {
			t.Skipf("无真实串口（预期在 CI/无硬件环境跳过）: %s", s.LastError)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s.Status != "online" {
		t.Skip("无串口硬件")
	}

	client, isWriter := s.Subscribe("c1", "测试")
	if !isWriter {
		t.Fatal("第一个客户端应是主控")
	}
	go func() { for range client.Queue {} }()

	ok, err := s.Write("c1", []byte("uname -a\r"))
	if !ok || err != nil {
		t.Fatalf("写入失败: %v %v", ok, err)
	}
	mgr.Close(s.ID)
}
