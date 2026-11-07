package server

import (
	"context"
	"sync"

	"diting/internal/engine"
)

type sseEvent struct {
	Type string
	Data any
}

type eventHub struct {
	mu      sync.Mutex
	clients map[chan sseEvent]struct{}
	cancel  context.CancelFunc
	resolve func(string) string
}

func newEventHub(ctx context.Context, source <-chan engine.Event, resolve func(string) string) *eventHub {
	ctx, cancel := context.WithCancel(ctx)
	hub := &eventHub{clients: make(map[chan sseEvent]struct{}), cancel: cancel, resolve: resolve}
	go func() {
		for {
			select {
			case <-ctx.Done():
				hub.close()
				return
			case event := <-source:
				hub.publish(engineSSEEvent(event, hub.resolve))
			}
		}
	}()
	return hub
}

func engineSSEEvent(event engine.Event, resolve func(string) string) sseEvent {
	switch event.Type {
	case "interim":
		return sseEvent{Type: event.Type, Data: event.Interim}
	case "segment":
		name := ""
		if resolve != nil && event.Segment.Speaker != "" {
			name = resolve(event.Segment.Speaker)
		}
		return sseEvent{Type: event.Type, Data: segmentView{Segment: event.Segment, SpeakerName: name}}
	default:
		return sseEvent{Type: event.Type, Data: event.Status}
	}
}

func (h *eventHub) subscribe() (<-chan sseEvent, func()) {
	client := make(chan sseEvent, 64)
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

func (h *eventHub) publish(event sseEvent) {
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
	h.clients = make(map[chan sseEvent]struct{})
}

func (h *eventHub) Close() {
	h.cancel()
}
