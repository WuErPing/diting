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
		{"https://api.xiaomimimo.com/v1", true},
		{"https://example.com/v1", true},
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
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":2,"category":"产品交付","title":"发布安排","summary":"讨论产品发布与验收。"}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "test-model"})
	groups, err := client.Group(context.Background(), []string{"你好", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0] != (Group{Start: 1, End: 2, Category: "产品交付", Title: "发布安排", Summary: "讨论产品发布与验收。"}) {
		t.Fatalf("groups = %v", groups)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "test-model" {
		t.Fatalf("model = %v", body["model"])
	}
	if body["temperature"] != float64(0) || body["max_tokens"] != float64(2048) {
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
	schemaBody, _ := schema["schema"].(map[string]any)
	properties, _ := schemaBody["properties"].(map[string]any)
	paragraphsSchema, _ := properties["paragraphs"].(map[string]any)
	items, _ := paragraphsSchema["items"].(map[string]any)
	itemProperties, _ := items["properties"].(map[string]any)
	if _, ok := itemProperties["category"]; !ok {
		t.Fatalf("topic schema lacks category: %v", itemProperties)
	}
	if _, ok := itemProperties["valuable"]; !ok {
		t.Fatalf("topic schema lacks valuable flag: %v", itemProperties)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	user, _ := messages[1].(map[string]any)
	prompt, _ := user["content"].(string)
	for _, want := range []string{"共 2 句", "1. 你好", "2. （无文字）", "1..2", "会议议题", "决策", "行动项", "寒暄", "category", "行内 Markdown", "valuable", "/no_think"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestClientCanonicalizeCategories(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(chatCompletion(`{"categories":[{"category":"薪酬绩效","sources":["薪酬激励","薪酬分配"]},{"category":"会议协调","sources":["会议安排"]}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "test-model"})
	mapping, err := client.CanonicalizeCategories(context.Background(), []CategoryTopics{
		{Category: "薪酬激励", Topics: []string{"调薪名单 讨论加薪范围。"}},
		{Category: "薪酬分配", Topics: []string{"额度分配 确认调薪额度。"}},
		{Category: "会议安排", Topics: []string{"日程协调 确定会议时间。"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(mapping) != 3 || mapping["薪酬激励"] != "薪酬绩效" || mapping["薪酬分配"] != "薪酬绩效" || mapping["会议安排"] != "会议协调" {
		t.Fatalf("mapping = %v", mapping)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	messages, _ := body["messages"].([]any)
	user, _ := messages[1].(map[string]any)
	prompt, _ := user["content"].(string)
	for _, want := range []string{"薪酬激励", "调薪名单", "薪酬分配", "会议安排"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestParseCategoryMappingRejectsDuplicateSources(t *testing.T) {
	_, err := parseCategoryMapping(`{"categories":[{"category":"薪酬绩效","sources":["薪酬激励"]},{"category":"人员管理","sources":["薪酬激励"]}]}`, []string{"薪酬激励"})
	if err == nil {
		t.Fatal("expected duplicate source error")
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
		{"plain", `{"paragraphs":[{"start":1,"end":2},{"start":3,"end":3}]}`, 3, []Group{{Start: 1, End: 2}, {Start: 3, End: 3}}, true},
		{"fenced", "```json\n{\"paragraphs\":[{\"start\":1,\"end\":3}]}\n```", 3, []Group{{Start: 1, End: 3}}, true},
		{"prose wrapped", `结果如下：{"paragraphs":[{"start":1,"end":3}]} 完毕。`, 3, []Group{{Start: 1, End: 3}}, true},
		{"float and string", `{"paragraphs":[{"start":1.0,"end":"3"}]}`, 3, []Group{{Start: 1, End: 3}}, true},
		{"low value", `{"paragraphs":[{"start":1,"end":3,"valuable":false}]}`, 3, []Group{{Start: 1, End: 3, TopicExcluded: true}}, true},
		{"valuable", `{"paragraphs":[{"start":1,"end":3,"valuable":true}]}`, 3, []Group{{Start: 1, End: 3}}, true},
		{"hierarchical topic", `{"paragraphs":[{"start":1,"end":3,"category":"产品交付","title":"发布安排","summary":"确定**周五**发布。"}]}`, 3, []Group{{Start: 1, End: 3, Category: "产品交付", Title: "发布安排", Summary: "确定**周五**发布。"}}, true},
		{"top level array", `[{"start":1,"end":3}]`, 3, nil, false},
		{"empty content", `   `, 3, nil, false},
		{"invalid json", `{"paragraphs":`, 3, nil, false},
		{"trailing gap", `{"paragraphs":[{"start":1,"end":2}]}`, 3, []Group{{Start: 1, End: 3}}, true},
		{"gap between groups", `{"paragraphs":[{"start":1,"end":3},{"start":5,"end":6}]}`, 6, []Group{{Start: 1, End: 3}, {Start: 4, End: 6}}, true},
		{"overlap", `{"paragraphs":[{"start":1,"end":2},{"start":2,"end":3}]}`, 3, []Group{{Start: 1, End: 2}, {Start: 3, End: 3}}, true},
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

func TestClientGroupRepairsGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":3},{"start":5,"end":6}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	groups, err := client.Group(context.Background(), []string{"一", "二", "三", "四", "五", "六"})
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0] != (Group{Start: 1, End: 3}) || groups[1] != (Group{Start: 4, End: 6}) {
		t.Fatalf("groups = %v", groups)
	}
}

func TestClientGroupRetriesEmptyChoicesOnce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[]}`))
			return
		}
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":1,"category":"产品","title":"发布","summary":"确定发布。","valuable":true}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	groups, err := client.Group(context.Background(), []string{"讨论发布"})
	if err != nil || len(groups) != 1 || calls != 2 {
		t.Fatalf("groups = %v, calls = %d, err = %v", groups, calls, err)
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
		{"invalid groups", http.StatusOK, `{"paragraphs":[{"start":1,"end":4}]}`, "stop"},
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

func TestClientErrorIncludesProviderMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API Key"}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := client.Group(context.Background(), []string{"讨论预算"})
	if err == nil || !strings.Contains(err.Error(), "Invalid API Key") {
		t.Fatalf("error = %v", err)
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
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":4}]}`, "stop")))
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

func TestClientCheckAllowsSlowModelListing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(3200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "test-model"})
	if err := client.Check(context.Background()); err != nil {
		t.Fatalf("slow but healthy model service was rejected: %v", err)
	}
}

func TestClientDefaults(t *testing.T) {
	client := NewClient(ClientConfig{})
	if client.Model() != "mimo-v2.6-flash" {
		t.Fatalf("model = %q", client.Model())
	}
	if client.baseURL != "https://token-plan-cn.xiaomimimo.com/v1" {
		t.Fatalf("base URL = %q", client.baseURL)
	}
}

func TestMiMoRequestUsesDocumentedParameters(t *testing.T) {
	client := NewClient(ClientConfig{})
	request := client.chatRequest([]string{"讨论预算"})
	if request.MaxTokens != 0 || request.MaxCompletionTokens != 2048 {
		t.Fatalf("token budget = max_tokens %d / max_completion_tokens %d", request.MaxTokens, request.MaxCompletionTokens)
	}
	if request.Thinking == nil || request.Thinking.Type != "disabled" {
		t.Fatalf("thinking = %+v", request.Thinking)
	}
	if request.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q", request.ReasoningEffort)
	}
}

func TestClientSendsAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "secret-key" {
			t.Errorf("api-key header = %q", r.Header.Get("api-key"))
		}
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":1}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, APIKey: "secret-key"})
	if _, err := client.Group(context.Background(), []string{"讨论发布计划"}); err != nil {
		t.Fatal(err)
	}
}

func TestClientSummarizeUsesCurrentModel(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(chatCompletion(" 会议确定了发布日期和验收负责人。 ", "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "summary-model"})
	summary, err := client.Summarize(context.Background(), "讨论发布日期和验收安排。")
	if err != nil {
		t.Fatal(err)
	}
	if summary != "会议确定了发布日期和验收负责人。" {
		t.Fatalf("summary = %q", summary)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "summary-model" {
		t.Fatalf("model = %v", body["model"])
	}
	messages, _ := body["messages"].([]any)
	user, _ := messages[1].(map[string]any)
	if !strings.Contains(user["content"].(string), "讨论发布日期和验收安排。") {
		t.Fatalf("prompt = %v", user["content"])
	}
}

func TestClientSetModelChangesRequests(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(chatCompletion(`{"paragraphs":[{"start":1,"end":1}]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "first"})
	if client.Model() != "first" {
		t.Fatalf("model = %q", client.Model())
	}
	client.SetModel("second")
	if client.Model() != "second" {
		t.Fatalf("model after set = %q", client.Model())
	}
	if _, err := client.Group(context.Background(), []string{"一"}); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "second" {
		t.Fatalf("request model = %v", body["model"])
	}
}

func TestClientModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
	}))
	defer server.Close()

	models, err := NewClient(ClientConfig{BaseURL: server.URL, Model: "a"}).Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("models = %v", models)
	}
}

func TestClientCorrectSendsExpectedRequest(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(chatCompletion(`{"texts":["摘要总结。","第二句。"]}`, "stop")))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL + "/v1", Model: "test-model"})
	corrected, err := client.Correct(context.Background(), []string{"在要总结。", "第二句。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(corrected) != 2 || corrected[0] != "摘要总结。" || corrected[1] != "第二句。" {
		t.Fatalf("corrected = %v", corrected)
	}

	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body["temperature"] != float64(0) {
		t.Fatalf("sampling = %v", body["temperature"])
	}
	if _, ok := body["reasoning_effort"]; ok {
		t.Fatalf("correction must keep thinking enabled: %v", body["reasoning_effort"])
	}
	format, _ := body["response_format"].(map[string]any)
	schema, _ := format["json_schema"].(map[string]any)
	props, _ := schema["schema"].(map[string]any)["properties"].(map[string]any)
	texts, _ := props["texts"].(map[string]any)
	if texts["minItems"] != float64(2) || texts["maxItems"] != float64(2) {
		t.Fatalf("texts schema = %v", texts)
	}
	messages, _ := body["messages"].([]any)
	system, _ := messages[0].(map[string]any)
	if !strings.Contains(system["content"].(string), "校对助手") {
		t.Fatalf("system = %v", system["content"])
	}
	user, _ := messages[1].(map[string]any)
	prompt := user["content"].(string)
	var decoded []string
	if err := json.Unmarshal([]byte(prompt), &decoded); err != nil {
		t.Fatalf("user content is not a JSON array: %s", prompt)
	}
	if len(decoded) != 2 || decoded[0] != "在要总结。" || decoded[1] != "第二句。" {
		t.Fatalf("prompt texts = %v", decoded)
	}
}

func TestClientCorrectClassifiesErrors(t *testing.T) {
	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion(`{"texts":["只有一句"]}`, "stop")))
	}))
	_, err := NewClient(ClientConfig{BaseURL: mismatch.URL, Model: "m"}).Correct(context.Background(), []string{"一", "二"})
	mismatch.Close()
	if !errors.Is(err, ErrInvalidCorrection) {
		t.Fatalf("count mismatch error = %v", err)
	}

	truncated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion(`{"texts":["一"]}`, "length")))
	}))
	_, err = NewClient(ClientConfig{BaseURL: truncated.URL, Model: "m"}).Correct(context.Background(), []string{"一"})
	truncated.Close()
	if !errors.Is(err, ErrInvalidCorrection) {
		t.Fatalf("truncated error = %v", err)
	}

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	downURL := down.URL
	down.Close()
	_, err = NewClient(ClientConfig{BaseURL: downURL, Model: "m", Timeout: 100 * time.Millisecond}).Correct(context.Background(), []string{"一"})
	if err == nil || errors.Is(err, ErrInvalidCorrection) {
		t.Fatalf("transport error = %v", err)
	}
}

func TestClientCorrectParsesFencedReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion("```json\n{\"texts\":[\"摘要总结。\"]}\n```", "stop")))
	}))
	defer server.Close()

	corrected, err := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"}).Correct(context.Background(), []string{"在要总结。"})
	if err != nil {
		t.Fatal(err)
	}
	if len(corrected) != 1 || corrected[0] != "摘要总结。" {
		t.Fatalf("corrected = %v", corrected)
	}
}

func decodeCorrectionPrompt(t *testing.T, body []byte) []string {
	t.Helper()
	var request struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) < 2 {
		t.Fatalf("messages = %d", len(request.Messages))
	}
	var texts []string
	if err := json.Unmarshal([]byte(request.Messages[1].Content), &texts); err != nil {
		t.Fatalf("user content is not a JSON array: %v", err)
	}
	return texts
}

func echoCorrected(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()
	texts := decodeCorrectionPrompt(t, body)
	out := make([]string, len(texts))
	for i, text := range texts {
		out[i] = strings.ReplaceAll(text, "错", "对")
	}
	raw, _ := json.Marshal(map[string][]string{"texts": out})
	_, _ = w.Write([]byte(chatCompletion(string(raw), "stop")))
}

func TestClientCorrectChunksLongInput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		echoCorrected(t, w, body)
	}))
	defer server.Close()

	long := strings.Repeat("这是错误的句子。", 100) // 800 runes -> multiple chunks
	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	corrected, err := client.Correct(context.Background(), []string{long})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected the long input to be chunked, calls = %d", calls.Load())
	}
	want := strings.Repeat("这是对误的句子。", 100)
	if len(corrected) != 1 || corrected[0] != want {
		t.Fatalf("reassembled correction mismatch (len %d)", len(corrected))
	}
}

func TestClientCorrectPartialChunkFailureKeepsOriginal(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(chatCompletion(`{"texts":[]}`, "length")))
			return
		}
		echoCorrected(t, w, body)
	}))
	defer server.Close()

	long := strings.Repeat("这是错误的句子。", 100)
	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	corrected, err := client.Correct(context.Background(), []string{long})
	if err != nil {
		t.Fatalf("a single failed chunk must not fail the batch: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
	text := corrected[0]
	if !strings.Contains(text, "错") {
		t.Fatal("the failed chunk should keep its original text")
	}
	if !strings.Contains(text, "对") {
		t.Fatal("the successful chunk should be corrected")
	}
	if len([]rune(text)) != len([]rune(long)) {
		t.Fatalf("length changed: got %d want %d", len([]rune(text)), len([]rune(long)))
	}
}

func TestClientCorrectAllChunksInvalidReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chatCompletion(`{"texts":[]}`, "length")))
	}))
	defer server.Close()

	long := strings.Repeat("这是错误的句子。", 100)
	client := NewClient(ClientConfig{BaseURL: server.URL, Model: "m"})
	_, err := client.Correct(context.Background(), []string{long})
	if !errors.Is(err, ErrInvalidCorrection) {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitSentencesRejoinsExactly(t *testing.T) {
	text := "第一句。第二句！第三句？第四句;结尾没有标点"
	units := splitSentences(text)
	if len(units) < 4 {
		t.Fatalf("units = %v", units)
	}
	if strings.Join(units, "") != text {
		t.Fatalf("rejoin mismatch: %q", strings.Join(units, ""))
	}
}

func TestChunkRangesRespectsBudget(t *testing.T) {
	units := []string{"aaaa", "bbb", "cc", strings.Repeat("x", 60)}
	ranges := chunkRanges(units, 10)
	// The 60-rune unit exceeds the budget and must form its own chunk.
	last := ranges[len(ranges)-1]
	if last.end-last.start != 1 || units[last.start] != strings.Repeat("x", 60) {
		t.Fatalf("oversized unit not isolated: %v", ranges)
	}
	for _, r := range ranges {
		total := 0
		for _, u := range units[r.start:r.end] {
			total += len([]rune(u))
		}
		if r.end-r.start > 1 && total > 10 {
			t.Fatalf("chunk %v exceeds budget: %d runes", r, total)
		}
	}
}
