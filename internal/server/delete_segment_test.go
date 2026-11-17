package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"diting/internal/paragraph"
	"diting/internal/store"
)

func TestHandleDeleteSegmentUpdatesMergedTranscript(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	t.Setenv("DITING_TRASH_DIR", t.TempDir())
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	paragraphStore, err := paragraph.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	var ids []string
	for i, text := range []string{"删除我", "保留我"} {
		started := base.Add(time.Duration(i) * time.Minute)
		id := started.Format("20060102-150405.000000000")
		ids = append(ids, id)
		if _, err := segmentStore.Save(store.Segment{
			ID: id, StartedAt: started, EndedAt: started.Add(time.Second), Text: text,
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := paragraphStore.SaveMany([]paragraph.Paragraph{{
		ID: "combined", StartedAt: base, EndedAt: base.Add(2 * time.Minute),
		Text: "合并文本", SegmentIDs: ids,
	}}); err != nil {
		t.Fatal(err)
	}
	reloaded := false
	server := &Server{
		store: segmentStore, paragraphs: paragraphStore, merge: true,
		onArchiveChange: func() { reloaded = true }, statsAt: time.Now(),
	}

	rejected := httptest.NewRequest(http.MethodDelete, "/api/segments/"+ids[0], nil)
	rejected.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleDeleteSegment(recorder, rejected)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodDelete, "/api/segments/invalid", nil)
	invalid.SetPathValue("id", "invalid")
	recorder = httptest.NewRecorder()
	server.handleDeleteSegment(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d", recorder.Code)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/segments/"+ids[0], nil)
	request.SetPathValue("id", ids[0])
	recorder = httptest.NewRecorder()
	server.handleDeleteSegment(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response deleteSegmentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != ids[0] || response.RemovedParagraphs != 1 || response.TrashPath == "" {
		t.Fatalf("response = %+v", response)
	}
	if !reloaded || !server.statsAt.IsZero() {
		t.Fatalf("reloaded = %v, statsAt = %v", reloaded, server.statsAt)
	}

	recorder = httptest.NewRecorder()
	server.handleMarkdown(recorder, httptest.NewRequest(http.MethodGet, "/api/markdown", nil))
	markdown := recorder.Body.String()
	if strings.Contains(markdown, "删除我") || strings.Contains(markdown, "合并文本") || !strings.Contains(markdown, "保留我") {
		t.Fatalf("merged transcript after delete:\n%s", markdown)
	}

	missing := httptest.NewRequest(http.MethodDelete, "/api/segments/"+ids[0], nil)
	missing.SetPathValue("id", ids[0])
	recorder = httptest.NewRecorder()
	server.handleDeleteSegment(recorder, missing)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing segment status = %d", recorder.Code)
	}
}

func TestHandleDeleteSegmentsUpdatesMergedTranscript(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	t.Setenv("DITING_TRASH_DIR", t.TempDir())
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	paragraphStore, err := paragraph.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	var ids []string
	for i, text := range []string{"删除一", "删除二", "保留我"} {
		started := base.Add(time.Duration(i) * time.Minute)
		id := started.Format("20060102-150405.000000000")
		ids = append(ids, id)
		if _, err := segmentStore.Save(store.Segment{
			ID: id, StartedAt: started, EndedAt: started.Add(time.Second), Text: text,
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := paragraphStore.SaveMany([]paragraph.Paragraph{
		{ID: "combined", StartedAt: base, EndedAt: base.Add(2 * time.Minute), Text: "合并文本", SegmentIDs: ids[:2]},
		{ID: "kept", StartedAt: base.Add(2 * time.Minute), EndedAt: base.Add(3 * time.Minute), Text: "保留段落", SegmentIDs: ids[2:]},
	}); err != nil {
		t.Fatal(err)
	}
	reloaded := false
	server := &Server{
		store: segmentStore, paragraphs: paragraphStore, merge: true,
		onArchiveChange: func() { reloaded = true }, statsAt: time.Now(),
	}

	post := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/segments/delete", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		server.handleDeleteSegments(recorder, request)
		return recorder
	}

	rejected := httptest.NewRequest(http.MethodPost, "/api/segments/delete", strings.NewReader(`{"ids":["x"]}`))
	rejected.Header.Set("Content-Type", "application/json")
	rejected.Header.Set("Origin", "http://evil.example")
	rejectedRecorder := httptest.NewRecorder()
	server.handleDeleteSegments(rejectedRecorder, rejected)
	if rejectedRecorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", rejectedRecorder.Code)
	}
	for _, body := range []string{`{}`, `{"ids":[]}`, `{"ids":["invalid"]}`, `{"ids":null,"extra":1}`} {
		if recorder := post(body); recorder.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d", body, recorder.Code)
		}
	}

	recorder := post(`{"ids":["` + ids[0] + `","` + ids[1] + `","` + ids[0] + `"]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response deleteSegmentsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 2 || response.RemovedParagraphs != 1 {
		t.Fatalf("response = %+v", response)
	}
	for _, result := range response.Data {
		if result.TrashPath == "" {
			t.Fatalf("result missing trash path: %+v", result)
		}
	}
	if !reloaded || !server.statsAt.IsZero() {
		t.Fatalf("reloaded = %v, statsAt = %v", reloaded, server.statsAt)
	}

	recorder = httptest.NewRecorder()
	server.handleMarkdown(recorder, httptest.NewRequest(http.MethodGet, "/api/markdown", nil))
	markdown := recorder.Body.String()
	if strings.Contains(markdown, "删除一") || strings.Contains(markdown, "删除二") || strings.Contains(markdown, "合并文本") || !strings.Contains(markdown, "保留段落") {
		t.Fatalf("merged transcript after batch delete:\n%s", markdown)
	}

	if recorder := post(`{"ids":["` + ids[0] + `"]}`); recorder.Code != http.StatusOK {
		t.Fatalf("repeat delete status = %d", recorder.Code)
	}
}
