package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"diting/internal/lexicon"
	"diting/internal/speaker"
	"diting/internal/store"
)

type fakeSpeakerDirectory struct {
	views     []speaker.View
	names     map[string]string
	setErr    error
	lastLabel string
	lastAlias string
}

func (f *fakeSpeakerDirectory) Speakers() []speaker.View {
	return append([]speaker.View(nil), f.views...)
}

func (f *fakeSpeakerDirectory) SpeakerName(label string) string {
	if name := f.names[label]; name != "" {
		return name
	}
	return label
}

func (f *fakeSpeakerDirectory) SetSpeakerAlias(label, alias string) error {
	f.lastLabel, f.lastAlias = label, alias
	if f.setErr != nil {
		return f.setErr
	}
	for i := range f.views {
		if f.views[i].Label == label {
			f.views[i].Name = alias
			if alias == "" {
				f.views[i].Name = label
			}
			if f.names == nil {
				f.names = map[string]string{}
			}
			f.names[label] = f.views[i].Name
			return nil
		}
	}
	return speaker.ErrUnknownSpeaker
}

func (f *fakeSpeakerDirectory) CanonicalSpeaker(label string) string {
	for _, view := range f.views {
		if view.Label == label && view.MergedInto != "" {
			return f.CanonicalSpeaker(view.MergedInto)
		}
	}
	return label
}

func (f *fakeSpeakerDirectory) SetSpeakerMerge(label, target string) error {
	if f.setErr != nil {
		return f.setErr
	}
	for i := range f.views {
		if f.views[i].Label == label {
			f.views[i].MergedInto = target
			return nil
		}
	}
	return speaker.ErrUnknownSpeaker
}

func newTestServerWithSegments(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { segmentStore.Close() })
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		started := base.Add(time.Duration(i) * 24 * time.Hour)
		if _, err := segmentStore.Save(store.Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(time.Second),
			Text:      "你好",
			Speaker:   "说话人1",
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{store: segmentStore}, segmentStore
}

func TestHandleDays(t *testing.T) {
	server, _ := newTestServerWithSegments(t)
	recorder := httptest.NewRecorder()
	server.handleDays(recorder, httptest.NewRequest(http.MethodGet, "/api/days", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response daysResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Days) != 2 || response.Days[0].Date != "2026-11-06" || response.Days[1].Date != "2026-11-07" {
		t.Fatalf("days = %+v", response.Days)
	}
}

func TestHandleDeleteDay(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	trash := t.TempDir()
	t.Setenv("DITING_TRASH_DIR", trash)
	segmentStore, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()
	base := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	for i := 0; i < 2; i++ {
		started := base.Add(time.Duration(i) * 24 * time.Hour)
		if _, err := segmentStore.Save(store.Segment{
			ID:        started.Format("20060102-150405.000000000"),
			StartedAt: started,
			EndedAt:   started.Add(time.Second),
			Text:      "你好",
		}, []float32{0.1}); err != nil {
			t.Fatal(err)
		}
	}

	reloaded := false
	server := &Server{store: segmentStore, onArchiveChange: func() { reloaded = true }, statsAt: time.Now()}

	rejected := httptest.NewRequest(http.MethodDelete, "/api/days/2026-11-06", nil)
	rejected.Header.Set("Origin", "http://evil.example")
	recorder := httptest.NewRecorder()
	server.handleDeleteDay(recorder, rejected)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/days/2026-11-06", nil)
	request.SetPathValue("date", "2026-11-06")
	recorder = httptest.NewRecorder()
	server.handleDeleteDay(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var response deleteDayResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Segments != 1 || response.TrashPath == "" {
		t.Fatalf("response = %+v", response)
	}
	if !reloaded {
		t.Fatal("archive change callback not called")
	}
	if !server.statsAt.IsZero() {
		t.Fatal("stats cache not invalidated")
	}

	bad := httptest.NewRequest(http.MethodDelete, "/api/days/2026-13-01", nil)
	bad.SetPathValue("date", "2026-13-01")
	recorder = httptest.NewRecorder()
	server.handleDeleteDay(recorder, bad)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid date status = %d", recorder.Code)
	}
}

func TestHandleSpeakersAndAlias(t *testing.T) {
	directory := &fakeSpeakerDirectory{
		views: []speaker.View{{Label: "说话人1", Name: "说话人1"}},
		names: map[string]string{},
	}
	server := &Server{speakers: directory}

	recorder := httptest.NewRecorder()
	server.handleSpeakers(recorder, httptest.NewRequest(http.MethodGet, "/api/speakers", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "说话人1") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/speakers/说话人1", strings.NewReader(`{"alias":"章总"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("label", "说话人1")
	recorder = httptest.NewRecorder()
	server.handleSpeakerAlias(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if directory.lastLabel != "说话人1" || directory.lastAlias != "章总" {
		t.Fatalf("set alias = %q %q", directory.lastLabel, directory.lastAlias)
	}
	if !strings.Contains(recorder.Body.String(), "章总") {
		t.Fatalf("body = %s", recorder.Body)
	}

	invalid := httptest.NewRequest(http.MethodPatch, "/api/speakers/nobody", strings.NewReader(`{"alias":"章总"}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.SetPathValue("label", "nobody")
	recorder = httptest.NewRecorder()
	server.handleSpeakerAlias(recorder, invalid)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid label status = %d", recorder.Code)
	}

	directory.setErr = speaker.ErrUnknownSpeaker
	missing := httptest.NewRequest(http.MethodPatch, "/api/speakers/说话人9", strings.NewReader(`{"alias":"章总"}`))
	missing.Header.Set("Content-Type", "application/json")
	missing.SetPathValue("label", "说话人9")
	recorder = httptest.NewRecorder()
	server.handleSpeakerAlias(recorder, missing)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing speaker status = %d", recorder.Code)
	}
}

func TestHandleLexicon(t *testing.T) {
	instance, err := lexicon.Open(filepath.Join(t.TempDir(), "lexicon.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{lexicon: instance}

	recorder := httptest.NewRecorder()
	server.handleLexicon(recorder, httptest.NewRequest(http.MethodGet, "/api/lexicon", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"rules":[]`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	request := httptest.NewRequest(http.MethodPut, "/api/lexicon", strings.NewReader(`{"rules":[{"from":"张总","to":"章总"}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleLexiconUpdate(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := instance.Apply("张总"); got != "章总" {
		t.Fatalf("apply = %q", got)
	}

	duplicate := httptest.NewRequest(http.MethodPut, "/api/lexicon", strings.NewReader(`{"rules":[{"from":"张总","to":"章总"},{"from":"张总","to":"王总"}]}`))
	duplicate.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	server.handleLexiconUpdate(recorder, duplicate)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate status = %d, body = %s", recorder.Code, recorder.Body)
	}

	crossOrigin := httptest.NewRequest(http.MethodPut, "/api/lexicon", strings.NewReader(`{"rules":[]}`))
	crossOrigin.Header.Set("Content-Type", "application/json")
	crossOrigin.Header.Set("Origin", "http://evil.example")
	recorder = httptest.NewRecorder()
	server.handleLexiconUpdate(recorder, crossOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", recorder.Code)
	}
}

func TestHandleSegmentsIncludesSpeakerName(t *testing.T) {
	server, segmentStore := newTestServerWithSegments(t)
	server.speakers = &fakeSpeakerDirectory{names: map[string]string{"说话人1": "章总"}}

	recorder := httptest.NewRecorder()
	server.handleSegments(recorder, httptest.NewRequest(http.MethodGet, "/api/segments?limit=50", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var response struct {
		Data []segmentView `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 2 || response.Data[0].SpeakerName != "章总" {
		t.Fatalf("segments = %+v", response.Data)
	}
	if segments, err := segmentStore.All(); err != nil || segments[0].Speaker != "说话人1" {
		t.Fatalf("stored speaker changed: %+v %v", segments, err)
	}
}

func TestHandleMarkdownUsesSpeakerAlias(t *testing.T) {
	server, _ := newTestServerWithSegments(t)
	server.speakers = &fakeSpeakerDirectory{names: map[string]string{"说话人1": "章总"}}

	recorder := httptest.NewRecorder()
	server.handleMarkdown(recorder, httptest.NewRequest(http.MethodGet, "/api/markdown", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "章总") || strings.Contains(body, "说话人1") {
		t.Fatalf("markdown = %s", body)
	}
}

func TestHandleAudioMIME(t *testing.T) {
	t.Setenv("DITING_AUDIO_FORMAT", "wav")
	root := t.TempDir()
	segmentStore, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer segmentStore.Close()

	id := "20261106-140000.000000000"
	relative := "audio/20261106/" + id + ".m4a"
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 11, 6, 14, 0, 0, 0, time.Local)
	if err := segmentStore.Rewrite([]store.Segment{{
		ID:        id,
		StartedAt: started,
		EndedAt:   started.Add(time.Second),
		AudioPath: relative,
	}}); err != nil {
		t.Fatal(err)
	}

	server := &Server{store: segmentStore}
	request := httptest.NewRequest(http.MethodGet, "/api/segments/"+id+"/audio", nil)
	request.SetPathValue("id", id)
	recorder := httptest.NewRecorder()
	server.handleAudio(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "audio/mp4" {
		t.Fatalf("content type = %q", got)
	}
	if got := recorder.Header().Get("Content-Disposition"); !strings.Contains(got, id+".m4a") {
		t.Fatalf("disposition = %q", got)
	}
}

func TestHandleSpeakerAliasRejectsUnknownField(t *testing.T) {
	directory := &fakeSpeakerDirectory{views: []speaker.View{{Label: "说话人1", Name: "说话人1"}}}
	server := &Server{speakers: directory}
	request := httptest.NewRequest(http.MethodPatch, "/api/speakers/说话人1", strings.NewReader(`{"alias":"章总","extra":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("label", "说话人1")
	recorder := httptest.NewRecorder()
	server.handleSpeakerAlias(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", recorder.Code)
	}
	if directory.lastLabel != "" {
		t.Fatalf("unexpected alias write: %q", directory.lastLabel)
	}
}
