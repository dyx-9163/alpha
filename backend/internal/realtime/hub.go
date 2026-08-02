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
	subscribers map[*subscriber]struct{}
}

func NewHub() *Hub {
	return &Hub{subscribers: map[*subscriber]struct{}{}}
}

const subscriberBufferSize = 64

type subscriber struct {
	events     chan Event
	gap        chan struct{}
	output     chan Event
	done       chan struct{}
	stopOnce   sync.Once
	gapPending bool
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
	for subscriber := range h.subscribers {
		select {
		case subscriber.events <- event:
		default:
			if !subscriber.gapPending {
				subscriber.gapPending = true
				subscriber.gap <- struct{}{}
			}
		}
	}
	h.mu.Unlock()
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	if h == nil {
		output := make(chan Event)
		close(output)
		return output, func() {}
	}
	subscriber := &subscriber{
		events: make(chan Event, subscriberBufferSize),
		gap:    make(chan struct{}, 1),
		output: make(chan Event),
		done:   make(chan struct{}),
	}
	h.mu.Lock()
	h.subscribers[subscriber] = struct{}{}
	h.mu.Unlock()
	go h.forward(subscriber)
	return subscriber.output, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[subscriber]; ok {
			delete(h.subscribers, subscriber)
			subscriber.stopOnce.Do(func() { close(subscriber.done) })
		}
		h.mu.Unlock()
	}
}

func (h *Hub) forward(subscriber *subscriber) {
	defer close(subscriber.output)
	for {
		select {
		case <-subscriber.done:
			return
		case <-subscriber.gap:
			if !sendSubscriberEvent(subscriber, overflowGapEvent()) {
				return
			}
			h.clearGap(subscriber)
			continue
		default:
		}

		select {
		case <-subscriber.done:
			return
		case <-subscriber.gap:
			if !sendSubscriberEvent(subscriber, overflowGapEvent()) {
				return
			}
			h.clearGap(subscriber)
		case event := <-subscriber.events:
			if !sendSubscriberEvent(subscriber, event) {
				return
			}
		}
	}
}

func (h *Hub) clearGap(subscriber *subscriber) {
	h.mu.Lock()
	if _, ok := h.subscribers[subscriber]; ok {
		subscriber.gapPending = false
	}
	h.mu.Unlock()
}

func sendSubscriberEvent(subscriber *subscriber, event Event) bool {
	select {
	case subscriber.output <- event:
		return true
	case <-subscriber.done:
		return false
	}
}

func overflowGapEvent() Event {
	return Event{
		Type:      "realtime.gap",
		Resource:  "status",
		Status:    "overflow",
		CreatedAt: time.Now().UTC(),
		Payload:   map[string]any{"reason": "subscriber_overflow"},
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
