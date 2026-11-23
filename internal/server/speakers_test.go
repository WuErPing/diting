package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"diting/internal/speaker"
	"diting/internal/store"
)

func TestHandleSpeakerSamplesReturnsLatestTranscribedSegmentPerLabel(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	base := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)
	segments := []store.Segment{
		{Text: "旧样板", Speaker: "说话人1"},
		{Text: "第二人的样板", Speaker: "说话人2"},
		{Text: "最新样板", Speaker: "说话人1"},
		{Text: "  ", Speaker: "说话人1"},
		{Text: "未登记声纹", Speaker: "说话人3"},
	}
	for i, segment := range segments {
		segment.StartedAt = base.Add(time.Duration(i) * time.Second)
		segment.EndedAt = segment.StartedAt.Add(time.Second)
		segment.ID = segment.StartedAt.Format("20060102-150405.000000000")
		segments[i] = segment
		if _, err := segmentStore.Save(segment, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}

	server := &Server{
		store: segmentStore,
		speakers: &fakeSpeakerDirectory{
			views: []speaker.View{{Label: "说话人1", Name: "章总"}, {Label: "说话人2", Name: "说话人2"}, {Label: "说话人4", Name: "说话人4"}},
			names: map[string]string{"说话人1": "章总"},
		},
	}
	recorder := httptest.NewRecorder()
	server.handleSpeakerSamples(recorder, httptest.NewRequest(http.MethodGet, "/api/speakers/samples", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var response struct {
		Data []struct {
			Label  string      `json:"label"`
			Sample segmentView `json:"sample"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 3 {
		t.Fatalf("samples = %+v", response.Data)
	}
	if got := response.Data[0].Sample; got.ID != segments[2].ID || got.Text != "最新样板" || got.SpeakerName != "章总" {
		t.Fatalf("speaker 1 sample = %+v", got)
	}
	if got := response.Data[1].Sample; got.ID != segments[1].ID || got.Text != "第二人的样板" {
		t.Fatalf("speaker 2 sample = %+v", got)
	}
	if response.Data[2].Sample.ID != "" {
		t.Fatalf("speaker without recordings received a sample: %+v", response.Data[2].Sample)
	}
}

func TestHandleSpeakerSamplesWithoutSpeakerDirectory(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.handleSpeakerSamples(recorder, httptest.NewRequest(http.MethodGet, "/api/speakers/samples", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"data\":[]}\n" {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
}
