package paragraph

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const usageObject = `{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":30},"completion_tokens_details":{"reasoning_tokens":20}}`

func completionWithUsage(content, finishReason string) string {
	payload := map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]any{"content": content},
			"finish_reason": finishReason,
		}},
		"usage": json.RawMessage(usageObject),
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func TestClientReportsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(completionWithUsage(`{"paragraphs":[{"start":1,"end":1,"title":"测试","summary":"测试议题。"}]}`, "stop")))
	}))
	defer server.Close()

	var gotModel string
	var gotUsage json.RawMessage
	calls := 0
	client := NewClient(ClientConfig{
		BaseURL: server.URL + "/v1",
		Model:   "test-model",
		OnUsage: func(model string, usage json.RawMessage, durationMs int64) {
			calls++
			gotModel = model
			gotUsage = usage
			if durationMs < 0 {
				t.Errorf("durationMs = %d", durationMs)
			}
		},
	})
	if _, err := client.Group(context.Background(), []string{"你好"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || gotModel != "test-model" {
		t.Fatalf("calls = %d, model = %q", calls, gotModel)
	}
	var u struct {
		PromptTokens int64 `json:"prompt_tokens"`
	}
	if err := json.Unmarshal(gotUsage, &u); err != nil || u.PromptTokens != 100 {
		t.Fatalf("usage = %s, err = %v", gotUsage, err)
	}
}

func TestClientReportsUsageOnTruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(completionWithUsage(`{"paragraphs":`, "length")))
	}))
	defer server.Close()

	calls := 0
	client := NewClient(ClientConfig{
		BaseURL: server.URL + "/v1",
		OnUsage: func(string, json.RawMessage, int64) { calls++ },
	})
	if _, err := client.Group(context.Background(), []string{"你好"}); err == nil {
		t.Fatal("截断响应应报错")
	}
	if calls != 1 {
		t.Fatalf("截断响应也应上报 usage，calls = %d", calls)
	}
}

func TestClientNilOnUsageWithUsagePresent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(completionWithUsage(`{"paragraphs":[{"start":1,"end":1,"title":"测试","summary":"测试议题。"}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL + "/v1"})
	if _, err := client.Group(context.Background(), []string{"你好"}); err != nil {
		t.Fatal(err)
	}
}
