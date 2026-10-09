package connector

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func init() { Register("mqtt", NewMQTT) }

type MQTTConnector struct {
	params map[string]interface{}
	client mqtt.Client
	queue  chan []byte
}

func NewMQTT(params map[string]interface{}) (Connector, error) {
	return &MQTTConnector{params: params, queue: make(chan []byte, 1024)}, nil
}

func (c *MQTTConnector) Open(ctx context.Context) error {
	host, _ := c.params["host"].(string)
	if host == "" {
		return fmt.Errorf("MQTT 未配置 host")
	}
	port := 1883
	if p, ok := c.params["port"].(float64); ok {
		port = int(p)
	}
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", host, port)).
		SetClientID(fmt.Sprintf("linkhub-%d", time.Now().UnixNano()))
	if u, _ := c.params["username"].(string); u != "" {
		opts.SetUsername(u)
	}
	if p, _ := c.params["password"].(string); p != "" {
		opts.SetPassword(p)
	}
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, m mqtt.Message) {
		line := fmt.Sprintf("[%s] %s\r\n", m.Topic(), string(m.Payload()))
		select {
		case c.queue <- []byte(line):
		default:
		}
	})
	c.client = mqtt.NewClient(opts)
	if token := c.client.Connect(); token.Wait() && token.Error() != nil {
		return fmt.Errorf("MQTT 连接失败: %w", token.Error())
	}
	topic, _ := c.params["topic_sub"].(string)
	if topic == "" {
		topic = "#"
	}
	if token := c.client.Subscribe(topic, 0, nil); token.Wait() && token.Error() != nil {
		return fmt.Errorf("MQTT 订阅失败: %w", token.Error())
	}
	return nil
}

func (c *MQTTConnector) Close() error {
	if c.client != nil {
		c.client.Disconnect(250)
	}
	return nil
}

func (c *MQTTConnector) Write(data []byte) error {
	if c.client == nil {
		return fmt.Errorf("MQTT 未打开")
	}
	topic, _ := c.params["topic_pub"].(string)
	if topic == "" {
		topic = "cmd"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			c.client.Publish(topic, 0, false, line)
		}
	}
	return nil
}

// Read 返回消息流（io.Reader 语义由 session 的 pump 驱动）
func (c *MQTTConnector) Read() (io.Reader, error) {
	return &mqttReader{queue: c.queue}, nil
}

type mqttReader struct {
	queue chan []byte
	buf   []byte
}

func (r *mqttReader) Read(p []byte) (int, error) {
	if len(r.buf) == 0 {
		r.buf = <-r.queue
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// Resize MQTT 无 PTY，空实现
func (c *MQTTConnector) Resize(rows, cols int) error { return nil }
