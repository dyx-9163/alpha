package realtime

import (
	"sync"
	"time"
)

type Event struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Resource    string         `json:"resource,omitempty"`
	ResourceID  string         `json:"resourceId,omitempty"`
	ServerID    string         `json:"serverId,omitempty"`
	InstanceID  string         `json:"instanceId,omitempty"`
	TaskID      string         `json:"taskId,omitempty"`
	Status      string         `json:"status,omitempty"`
	Version     int64          `json:"version,omitempty"`
	CollectedAt time.Time      `json:"collectedAt,omitempty"`
	CreatedAt   time.Time      `json:"createdAt"`
	Payload     map[string]any `json:"payload,omitempty"`
}

type Hub struct {
	mu          sync.Mutex
	next        int64
	subscribers map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subscribers: map[chan Event]struct{}{}}
}

func (h *Hub) Publish(event Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.next++
	if event.ID == "" {
		event.ID = time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + itoa64(h.next)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	subscribers := make([]chan Event, 0, len(h.subscribers))
	for ch := range h.subscribers {
		subscribers = append(subscribers, ch)
	}
	h.mu.Unlock()
	for _, ch := range subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	if h == nil {
		close(ch)
		return ch, func() {}
	}
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

func itoa64(value int64) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	n := value
	if n < 0 {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if value < 0 {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
