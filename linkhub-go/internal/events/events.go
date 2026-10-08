package events

import "sync"

// Bus 全局事件总线：session_status / device_changed / node_status / task_ran / resource_update
// 订阅者：/ws/events（前端）、集群缓存同步
type Bus struct {
	mu   sync.RWMutex
	subs map[int]chan Event
	next int
}

type Event struct {
	Name string                 `json:"event"`
	Data map[string]interface{} `json:"data"`
}

func NewBus() *Bus {
	return &Bus{subs: map[int]chan Event{}}
}

// Subscribe 注册订阅者，返回取消函数
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	id := b.next
	b.next++
	b.subs[id] = ch
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}
}

// Publish 向所有订阅者广播（非阻塞，慢消费者丢事件）
func (b *Bus) Publish(name string, data map[string]interface{}) {
	if data == nil {
		data = map[string]interface{}{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- Event{Name: name, Data: data}:
		default:
		}
	}
}
