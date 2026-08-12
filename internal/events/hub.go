package events

import (
	"sync"

	"eink-server/internal/store"
)

type Hub struct {
	mu          sync.Mutex
	next        int
	subscribers map[int]chan store.Event
}

func New() *Hub { return &Hub{subscribers: make(map[int]chan store.Event)} }

func (h *Hub) Subscribe() (<-chan store.Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.next
	h.next++
	ch := make(chan store.Event, 32)
	h.subscribers[id] = ch
	return ch, func() {
		h.mu.Lock()
		if c, ok := h.subscribers[id]; ok {
			delete(h.subscribers, id)
			close(c)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(e store.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}
