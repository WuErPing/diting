package server

import (
	"context"
	"testing"
	"time"

	"diting/internal/engine"
	"diting/internal/paragraph"
	"diting/internal/store"
)

func TestEngineSSEEventMapping(t *testing.T) {
	interim := engineSSEEvent(engine.Event{Type: "interim", Interim: engine.Interim{Text: "临时"}})
	if interim.Type != "interim" || interim.Data != (engine.Interim{Text: "临时"}) {
		t.Fatalf("interim = %+v", interim)
	}

	segment := store.Segment{ID: "s1", Text: "一句"}
	segmentEvent := engineSSEEvent(engine.Event{Type: "segment", Segment: segment})
	if segmentEvent.Type != "segment" || segmentEvent.Data != segment {
		t.Fatalf("segment = %+v", segmentEvent)
	}

	status := engine.Status{SegmentCount: 3}
	statusEvent := engineSSEEvent(engine.Event{Type: "status", Status: status})
	if statusEvent.Type != "status" || statusEvent.Data != status {
		t.Fatalf("status = %+v", statusEvent)
	}
}

func TestEventHubForwardsParagraphEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := make(chan engine.Event, 4)
	hub := newEventHub(ctx, source)
	defer hub.Close()
	client, unsubscribe := hub.subscribe()
	defer unsubscribe()

	source <- engine.Event{Type: "interim", Interim: engine.Interim{Text: "临时"}}
	select {
	case event := <-client:
		if event.Type != "interim" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("engine event not forwarded")
	}

	server := &Server{hub: hub}
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	server.PublishParagraph(paragraph.Paragraph{
		ID:         "p1",
		StartedAt:  start,
		EndedAt:    start.Add(5 * time.Second),
		Speaker:    "说话人1",
		Text:       "不应出现在事件里",
		SegmentIDs: []string{"a", "b"},
		Source:     paragraph.SourceLLM,
	})

	select {
	case event := <-client:
		if event.Type != "paragraph" {
			t.Fatalf("event = %+v", event)
		}
		payload, ok := event.Data.(paragraphEvent)
		if !ok {
			t.Fatalf("payload = %T", event.Data)
		}
		if payload.ID != "p1" || payload.SegmentCount != 2 || payload.Source != paragraph.SourceLLM || payload.Speaker != "说话人1" {
			t.Fatalf("payload = %+v", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("paragraph event not forwarded")
	}
}

func TestPublishParagraphWithoutHub(t *testing.T) {
	server := &Server{}
	server.PublishParagraph(paragraph.Paragraph{ID: "p1"})
}
