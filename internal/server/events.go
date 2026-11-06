package server

import (
	"context"
	"sync"

	"diting/internal/engine"
)

type eventHub struct {
	mu      sync.Mutex
	clients map[chan engine.Event]struct{}
	cancel  context.CancelFunc
}

func newEventHub(ctx context.Context, source <-chan engine.Event) *eventHub {
	ctx, cancel := context.WithCancel(ctx)
	hub := &eventHub{clients: make(map[chan engine.Event]struct{}), cancel: cancel}
	go func() {
		for {
			select {
			case <-ctx.Done():
				hub.close()
				return
			case event := <-source:
				hub.publish(event)
			}
		}
	}()
	return hub
}

func (h *eventHub) subscribe() (<-chan engine.Event, func()) {
	client := make(chan engine.Event, 64)
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return client, func() {
		once.Do(func() {
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client)
			}
			h.mu.Unlock()
		})
	}
}

func (h *eventHub) publish(event engine.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		select {
		case client <- event:
		default:
			delete(h.clients, client)
			close(client)
		}
	}
}

func (h *eventHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		close(client)
	}
	h.clients = make(map[chan engine.Event]struct{})
}

func (h *eventHub) Close() {
	h.cancel()
}
