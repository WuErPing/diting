package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"diting/internal/paragraph"
	"diting/internal/store"
)

func TestHandleCorrectParagraph(t *testing.T) {
	id := "20261110-100000.000000000"
	settings := &fakeCorrectionSettings{result: paragraph.CorrectionResult{Paragraphs: 1, Changed: 1}}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/"+id+"/correct", nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleCorrectParagraph(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.calls != 1 || settings.paragraphID != id {
		t.Fatalf("calls = %d, paragraph ID = %q", settings.calls, settings.paragraphID)
	}

	settings.err = paragraph.ErrParagraphNotFound
	recorder = httptest.NewRecorder()
	server.handleCorrectParagraph(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing paragraph status = %d", recorder.Code)
	}

	invalid := httptest.NewRequest(http.MethodPost, "/api/paragraphs/bad/correct", nil)
	invalid.SetPathValue("id", "bad")
	recorder = httptest.NewRecorder()
	server.handleCorrectParagraph(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid ID status = %d", recorder.Code)
	}
}

func TestHandleRetranscribeParagraph(t *testing.T) {
	id := "20261110-100000.000000000"
	settings := &fakeCorrectionSettings{}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/"+id+"/asr", nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleRetranscribeParagraph(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.calls != 1 || settings.paragraphID != id {
		t.Fatalf("calls = %d, paragraph ID = %q", settings.calls, settings.paragraphID)
	}
	var response paragraph.ASRResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Paragraphs != 1 || response.Segments != 2 || response.Changed != 2 {
		t.Fatalf("response = %+v", response)
	}

	settings.err = paragraph.ErrParagraphNotFound
	recorder = httptest.NewRecorder()
	server.handleRetranscribeParagraph(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing paragraph status = %d", recorder.Code)
	}
}

func TestHandleSummarizeParagraph(t *testing.T) {
	id := "20261110-100000.000000000"
	settings := &fakeCorrectionSettings{}
	server := &Server{settings: settings}
	request := httptest.NewRequest(http.MethodPost, "/api/paragraphs/"+id+"/summarize", nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleSummarizeParagraph(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if settings.calls != 1 || settings.paragraphID != id {
		t.Fatalf("calls = %d, paragraph ID = %q", settings.calls, settings.paragraphID)
	}
	var response paragraph.SummaryResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Paragraphs != 1 || response.Summary != "测试总结" {
		t.Fatalf("response = %+v", response)
	}

	settings.err = paragraph.ErrParagraphNotFound
	recorder = httptest.NewRecorder()
	server.handleSummarizeParagraph(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing paragraph status = %d", recorder.Code)
	}
}

func TestHandleParagraphsFiltersByStartDate(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	previousStart := time.Date(2026, 11, 9, 23, 59, 0, 0, time.Local)
	dayStart := time.Date(2026, 11, 10, 0, 2, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "p", StartedAt: previousStart, EndedAt: previousStart.Add(time.Second)},
		{ID: "q", StartedAt: previousStart.Add(time.Minute), EndedAt: previousStart.Add(2 * time.Minute)},
		{ID: "a", StartedAt: dayStart, EndedAt: dayStart.Add(time.Second)},
		{ID: "b", StartedAt: dayStart.Add(time.Minute), EndedAt: dayStart.Add(2 * time.Minute)},
	}
	if err := segmentStore.Rewrite(segments); err != nil {
		t.Fatal(err)
	}
	paragraphStore, err := paragraph.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paragraphStore.SaveMany([]paragraph.Paragraph{
		{ID: "p", StartedAt: segments[0].StartedAt, EndedAt: segments[1].EndedAt, SegmentIDs: []string{"p", "q"}},
		{ID: "a", StartedAt: segments[2].StartedAt, EndedAt: segments[2].EndedAt, SegmentIDs: []string{"a"}},
		{ID: "b", StartedAt: segments[3].StartedAt, EndedAt: segments[3].EndedAt, SegmentIDs: []string{"b"}},
	}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: segmentStore, paragraphs: paragraphStore, merge: true}
	recorder := httptest.NewRecorder()
	server.handleParagraphs(recorder, httptest.NewRequest(http.MethodGet, "/api/paragraphs?date=2026-11-10&limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 2 || response.Data[0].ID != "a" || response.Data[1].ID != "b" {
		t.Fatalf("filtered paragraphs = %+v", response.Data)
	}

	recorder = httptest.NewRecorder()
	server.handleParagraphs(recorder, httptest.NewRequest(http.MethodGet, "/api/paragraphs?date=2026-02-30", nil))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d", recorder.Code)
	}
}

func TestHandleParagraphsRecent(t *testing.T) {
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	base := time.Date(2026, 11, 10, 10, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{ID: "a", StartedAt: base, EndedAt: base.Add(time.Second)},
		{ID: "b", StartedAt: base.Add(2 * time.Second), EndedAt: base.Add(3 * time.Second)},
		{ID: "c", StartedAt: base.Add(4 * time.Second), EndedAt: base.Add(5 * time.Second)},
	}
	if err := segmentStore.Rewrite(segments); err != nil {
		t.Fatal(err)
	}
	paragraphStore, err := paragraph.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := paragraphStore.SaveMany([]paragraph.Paragraph{
		{ID: "a", StartedAt: segments[0].StartedAt, EndedAt: segments[0].EndedAt, SegmentIDs: []string{"a"}},
		{ID: "b", StartedAt: segments[1].StartedAt, EndedAt: segments[2].EndedAt, Text: "讨论发布与验收。", SegmentIDs: []string{"b", "c"}, Title: "发布安排", Summary: "讨论发布与验收。"},
	}); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: segmentStore, paragraphs: paragraphStore, merge: true}
	recorder := httptest.NewRecorder()
	server.handleParagraphs(recorder, httptest.NewRequest(http.MethodGet, "/api/paragraphs?limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response struct {
		Data []struct {
			ID        string    `json:"id"`
			StartedAt time.Time `json:"startedAt"`
			EndedAt   time.Time `json:"endedAt"`
			Title     string    `json:"title"`
			Summary   string    `json:"summary"`
			HasTopic  bool      `json:"hasTopic"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "b" || !response.Data[0].EndedAt.Equal(segments[2].EndedAt) ||
		response.Data[0].Title != "发布安排" || response.Data[0].Summary != "讨论发布与验收。" || !response.Data[0].HasTopic {
		t.Fatalf("response = %+v", response)
	}
}
