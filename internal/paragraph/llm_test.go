package paragraph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func chatCompletion(content, finishReason string) string {
	payload := map[string]any{
		"choices": []map[string]any{{
			"message":       map[string]any{"content": content},
			"finish_reason": finishReason,
		}},
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"http://127.0.0.1:1234/v1", true},
		{"http://localhost:1234/v1", true},
		{"https://LOCALHOST:1234/v1", true},
		{"http://[::1]:1234/v1", true},
		{"http://example.com:1234/v1", false},
		{"http://0.0.0.0:1234/v1", false},
		{"ftp://127.0.0.1:1234/v1", false},
		{"http://user:pass@127.0.0.1:1234/v1", false},
		{"://bad", false},
	}
	for _, tc := range cases {
		err := ValidateBaseURL(tc.url)
		if tc.ok && err != nil {
			t.Fatalf("%s: unexpected error %v", tc.url, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%s: expected error", tc.url)
		}
	}
}

func TestClientGroupSendsExpectedRequest(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":2}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "test-model"})
	groups, err := client.Group(context.Background(), []string{"你好", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0] != (Group{Start: 1, End: 2}) {
		t.Fatalf("groups = %v", groups)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "test-model" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["temperature"] != float64(0) || body["max_tokens"] != float64(1024) {
		t.Fatalf("sampling = %v / %v", body["temperature"], body["max_tokens"])
	}
	if body["stream"] != false || body["reasoning_effort"] != "none" {
		t.Fatalf("stream/reasoning = %v / %v", body["stream"], body["reasoning_effort"])
	}
	format, ok := body["response_format"].(map[string]any)
	if !ok || format["type"] != "json_schema" {
		t.Fatalf("response_format = %v", body["response_format"])
	}
	schema, _ := format["json_schema"].(map[string]any)
	if schema["strict"] != true {
		t.Fatalf("json_schema = %v", schema)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	user, _ := messages[1].(map[string]any)
	prompt, _ := user["content"].(string)
	for _, want := range []string{"共 2 句", "1. 你好", "2. （无文字）", "1..2", "/no_think"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestParseGroupsVariants(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		n    int
		want []Group
		ok   bool
	}{
		{"plain", `{"paragraphs":[{"start":1,"end":2},{"start":3,"end":3}]}`, 3, []Group{{1, 2}, {3, 3}}, true},
		{"fenced", "```json\n{\"paragraphs\":[{\"start\":1,\"end\":3}]}\n```", 3, []Group{{1, 3}}, true},
		{"prose wrapped", `结果如下：{"paragraphs":[{"start":1,"end":3}]} 完毕。`, 3, []Group{{1, 3}}, true},
		{"float and string", `{"paragraphs":[{"start":1.0,"end":"3"}]}`, 3, []Group{{1, 3}}, true},
		{"top level array", `[{"start":1,"end":3}]`, 3, nil, false},
		{"empty content", `   `, 3, nil, false},
		{"invalid json", `{"paragraphs":`, 3, nil, false},
		{"partial coverage", `{"paragraphs":[{"start":1,"end":2}]}`, 3, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groups, err := parseGroups(tc.raw, tc.n)
			if tc.ok {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(groups) != len(tc.want) {
					t.Fatalf("groups = %v", groups)
				}
				for i := range groups {
					if groups[i] != tc.want[i] {
						t.Fatalf("groups = %v, want %v", groups, tc.want)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got %v", groups)
			}
		})
	}
}

func TestClientGroupErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		content string
		finish  string
	}{
		{"http error", http.StatusInternalServerError, "", "stop"},
		{"truncated", http.StatusOK, `{"paragraphs":[]}`, "length"},
		{"no choices", http.StatusOK, "", "stop"},
		{"invalid groups", http.StatusOK, `{"paragraphs":[{"start":2,"end":3}]}`, "stop"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.name == "no choices" {
					_, _ = w.Write([]byte(`{"choices":[]}`))
					return
				}
				_, _ = w.Write([]byte(chatCompletion(tc.content, tc.finish)))
			}))
			defer server.Close()

			client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m", Timeout: 2 * time.Second})
			if _, err := client.Group(context.Background(), []string{"一", "二", "三"}); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestClientGroupRetriesWithoutResponseFormat(t *testing.T) {
	var calls atomic.Int32
	var secondBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"response_format is not supported"}`))
		default:
			secondBody = body
			_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":2}]}`, "stop")))
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	groups, err := client.Group(context.Background(), []string{"一", "二"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %v", groups)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	if strings.Contains(string(secondBody), "response_format") {
		t.Fatalf("retry still includes response_format: %s", secondBody)
	}
}

func TestClientGroupTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":1}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m", Timeout: 30 * time.Millisecond})
	if _, err := client.Group(context.Background(), []string{"一"}); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientGroupClassifiesErrors(t *testing.T) {
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":2,"end":3}]}`, "stop")))
	}))
	_, err := NewClient(ClientConfig{BaseURL: invalid.URL, Model: "m"}).Group(context.Background(), []string{"一", "二", "三"})
	invalid.Close()
	if !errors.Is(err, ErrInvalidGrouping) {
		t.Fatalf("invalid grouping error = %v", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	downURL := down.URL
	down.Close()
	_, err = NewClient(ClientConfig{BaseURL: downURL, Model: "m", Timeout: 100 * time.Millisecond}).Group(context.Background(), []string{"一"})
	if err == nil || errors.Is(err, ErrInvalidGrouping) {
		t.Fatalf("transport error = %v", err)
	}
}

func TestClientCheck(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"},{"id":"other"}]}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "test-model"})
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("check failed: %v", err)
	}

	missing := NewClient(ClientConfig{BaseURL: server.URL, Model: "absent"})
	if err := missing.Check(context.Background()); err == nil {
		t.Fatal("expected missing model error")
	}

	status.Store(http.StatusInternalServerError)
	if err := client.Check(context.Background()); err == nil {
		t.Fatal("expected status error")
	}
}

func TestClientModelDefault(t *testing.T) {
	client := NewClient(ClientConfig{})
	if client.Model() != DefaultModel {
		t.Fatalf("model = %q", client.Model())
	}
}
