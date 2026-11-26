package paragraph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"diting/internal/store"
)

func TestClientSummarizeDayReturnsMarkdown(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(chatCompletion("```markdown\n# 2026-11-07 会议纪要\n\n## 决策事项\n\n- 周五发布。\n```", "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "minutes-model"})
	markdown, err := client.SummarizeDay(context.Background(), "2026-11-07", "[09:00:00] 章总: 周五发布。")
	if err != nil {
		t.Fatal(err)
	}
	want := "# 2026-11-07 会议纪要\n\n## 决策事项\n\n- 周五发布。"
	if markdown != want {
		t.Fatalf("markdown = %q, want %q", markdown, want)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "minutes-model" {
		t.Fatalf("model = %v", body["model"])
	}
	messages, _ := body["messages"].([]any)
	system, _ := messages[0].(map[string]any)
	user, _ := messages[1].(map[string]any)
	if !strings.Contains(system["content"].(string), "Markdown") {
		t.Fatalf("system prompt = %v", system["content"])
	}
	if !strings.Contains(user["content"].(string), "2026-11-07") || !strings.Contains(user["content"].(string), "周五发布") {
		t.Fatalf("user prompt = %v", user["content"])
	}
}

type fakeDaySummarizer struct {
	markdown string
	date     string
	input    string
}

func (f *fakeDaySummarizer) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (f *fakeDaySummarizer) SummarizeDay(_ context.Context, date, text string) (string, error) {
	f.date, f.input = date, text
	return f.markdown, nil
}

func TestWorkerSummarizeDayUsesOnlySelectedDaysText(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	segments := []store.Segment{
		testSegment("previous", "说话人1", "前一天", start.AddDate(0, 0, -1), start.AddDate(0, 0, -1).Add(time.Second)),
		testSegment("a", "说话人1", "确定周五发布。", start, start.Add(time.Second)),
		testSegment("blank", "说话人2", "  ", start.Add(2*time.Second), start.Add(3*time.Second)),
		testSegment("filler", "说话人13", "嗯。嗯。嗯嗯。", start.Add(3*time.Second), start.Add(4*time.Second)),
		testSegment("b", "说话人2", "章总负责验收。", start.Add(4*time.Second), start.Add(5*time.Second)),
	}
	summarizer := &fakeDaySummarizer{markdown: "# 2026-11-07 会议纪要\n"}
	worker := NewWorker(WorkerOptions{Segments: fakeSegments(segments), Summarizer: summarizer, Logf: quietLogf})
	worker.SetSpeakerResolver(func(label string) string {
		if label == "说话人1" {
			return "章总"
		}
		return label
	})

	result, err := worker.SummarizeDay(context.Background(), "2026-11-07")
	if err != nil {
		t.Fatal(err)
	}
	if result.Date != "2026-11-07" || result.Segments != 2 || result.Markdown != summarizer.markdown {
		t.Fatalf("result = %+v", result)
	}
	if summarizer.date != "2026-11-07" {
		t.Fatalf("date = %q", summarizer.date)
	}
	for _, want := range []string{"[09:00:00] 章总: 确定周五发布。", "[09:00:04] 说话人2: 章总负责验收。"} {
		if !strings.Contains(summarizer.input, want) {
			t.Fatalf("input missing %q:\n%s", want, summarizer.input)
		}
	}
	for _, unwanted := range []string{"前一天", "嗯。嗯。嗯嗯。"} {
		if strings.Contains(summarizer.input, unwanted) {
			t.Fatalf("input contains %q:\n%s", unwanted, summarizer.input)
		}
	}
}

func TestWorkerSummarizeDayRejectsFillerOnlyTranscripts(t *testing.T) {
	start := time.Date(2026, 11, 7, 9, 0, 0, 0, time.Local)
	segments := []store.Segment{
		testSegment("a", "说话人13", "嗯。", start, start.Add(time.Second)),
		testSegment("b", "说话人13", "嗯。嗯嗯。", start.Add(2*time.Second), start.Add(3*time.Second)),
	}
	worker := NewWorker(WorkerOptions{
		Segments: fakeSegments(segments), Summarizer: &fakeDaySummarizer{}, Logf: quietLogf,
	})
	if _, err := worker.SummarizeDay(context.Background(), "2026-11-07"); !errors.Is(err, ErrDayNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkerSummarizeDayRequiresTranscripts(t *testing.T) {
	worker := NewWorker(WorkerOptions{
		Segments:   fakeSegments{},
		Summarizer: &fakeDaySummarizer{},
		Logf:       quietLogf,
	})
	if _, err := worker.SummarizeDay(context.Background(), "2026-11-07"); !errors.Is(err, ErrDayNotFound) {
		t.Fatalf("error = %v", err)
	}

	worker = NewWorker(WorkerOptions{Segments: fakeSegments{}, Logf: quietLogf})
	if _, err := worker.SummarizeDay(context.Background(), "2026-11-07"); !errors.Is(err, ErrDaySummaryUnavailable) {
		t.Fatalf("unavailable error = %v", err)
	}
}
