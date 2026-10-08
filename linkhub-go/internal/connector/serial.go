package connector

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.bug.st/serial"
)

func init() { Register("serial", NewSerial) }

type SerialConnector struct {
	port serial.Port
}

func NewSerial(params map[string]interface{}) (Connector, error) {
	return &SerialConnector{}, nil
}

func (c *SerialConnector) Open(ctx context.Context) error {
	return fmt.Errorf("serial connector requires params; use OpenWithParams")
}

func (c *SerialConnector) OpenWithParams(params map[string]interface{}) error {
	portName, _ := params["port"].(string)
	if portName == "" {
		return fmt.Errorf("串口未配置 port")
	}
	baud := 115200
	if b, ok := params["baudrate"].(float64); ok {
		baud = int(b)
	}
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit,
	}
	if bs, ok := params["bytesize"].(float64); ok {
		mode.DataBits = int(bs)
	}
	if p, ok := params["parity"].(string); ok {
		switch p {
		case "E": mode.Parity = serial.EvenParity
		case "O": mode.Parity = serial.OddParity
		}
	}
	if sb, ok := params["stopbits"].(float64); ok && int(sb) == 2 {
		mode.StopBits = serial.TwoStopBits
	}
	p, err := serial.Open(portName, mode)
	if err != nil {
		return fmt.Errorf("打开串口 %s 失败: %w", portName, err)
	}
	c.port = p
	return nil
}

func (c *SerialConnector) Close() error {
	if c.port != nil {
		return c.port.Close()
	}
	return nil
}

func (c *SerialConnector) Write(data []byte) error {
	if c.port == nil {
		return fmt.Errorf("串口未打开")
	}
	_, err := c.port.Write(data)
	return err
}

func (c *SerialConnector) Read() (io.Reader, error) {
	if c.port == nil {
		return nil, fmt.Errorf("串口未打开")
	}
	c.port.SetReadTimeout(time.Millisecond * 500) // 非阻塞读，由上层驱动循环
	return c.port, nil
}

// ListPorts 枚举本机串口
type PortInfo struct {
	Device      string `json:"device"`
	Description string `json:"description"`
	IsUSB       bool   `json:"is_usb"`
}

func ListPorts() []PortInfo {
	ports, _ := serial.GetPortsList()
	out := []PortInfo{}
	for _, p := range ports {
		out = append(out, PortInfo{Device: p, IsUSB: true})
	}
	return out
}
