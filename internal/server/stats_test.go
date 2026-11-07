package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"diting/internal/store"
)

func TestHandleStats(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	started := time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)
	if _, err := segmentStore.Save(store.Segment{
		ID:        "20261107-100000.000000000",
		StartedAt: started,
		EndedAt:   started.Add(3 * time.Second),
		Text:      "统计一下",
	}, []float32{0.2, -0.1}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: segmentStore}
	recorder := httptest.NewRecorder()
	server.handleStats(recorder, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		AudioFiles      int       `json:"audioFiles"`
		AudioBytes      int64     `json:"audioBytes"`
		Segments        int       `json:"segments"`
		TranscriptChars int       `json:"transcriptChars"`
		UpdatedAt       time.Time `json:"updatedAt"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.AudioFiles != 1 || response.Segments != 1 || response.TranscriptChars != 4 {
		t.Fatalf("stats = %+v", response)
	}
	if response.AudioBytes <= 0 || response.UpdatedAt.IsZero() {
		t.Fatalf("stats = %+v", response)
	}
}
