package paragraph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "http://127.0.0.1:1234/v1"
	DefaultModel   = "qwen3.6-35b-a3b-splash"
)

// ErrInvalidGrouping marks a fast, malformed model reply. It must not trigger
// the unavailability cooldown, which exists for hanging or unreachable servers.
var ErrInvalidGrouping = errors.New("LM Studio 段落划分无效")

// ErrInvalidCorrection marks a malformed correction reply; like ErrInvalidGrouping
// it must not trigger the unavailability cooldown.
var ErrInvalidCorrection = errors.New("LM Studio 语义修正无效")

var (
	errNoChoices = errors.New("LM Studio returned no choices")
	errTruncated = errors.New("LM Studio response truncated (finish_reason=length)")
)

const (
	maxResponseBytes = 1 << 20
	requestTimeout   = 90 * time.Second
)

const systemPrompt = "你是中文转写的段落切分助手。只决定哪些连续编号属于同一语义段落。" +
	"必须完整覆盖 1..N、连续且无重叠；不新增、删除或改写文字；只输出 JSON。"

type ClientConfig struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

func NewClient(cfg ClientConfig) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = requestTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		model:   cfg.Model,
		http:    &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *Client) Model() string {
	return c.model
}

// ValidateBaseURL keeps transcripts on the local machine: only loopback LM
// Studio endpoints are allowed.
func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid LM Studio URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("LM Studio URL must use http or https, got %q", parsed.Scheme)
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("LM Studio URL must be loopback, got %q", host)
	}
	if parsed.User != nil {
		return fmt.Errorf("LM Studio URL must not contain credentials")
	}
	return nil
}

func (c *Client) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("LM Studio /models returned %s", response.Status)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return fmt.Errorf("decode LM Studio /models: %w", err)
	}
	for _, model := range payload.Data {
		if model.ID == c.model {
			return nil
		}
	}
	return fmt.Errorf("LM Studio 未找到模型 %q", c.model)
}

func (c *Client) Group(ctx context.Context, texts []string) ([]Group, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("没有可切分的语句")
	}
	content, err := c.complete(ctx, c.chatRequest(texts))
	if errors.Is(err, errNoChoices) || errors.Is(err, errTruncated) {
		return nil, invalidGrouping(err)
	}
	if err != nil {
		return nil, err
	}
	groups, err := parseGroups(content, len(texts))
	if err != nil {
		return nil, invalidGrouping(err)
	}
	return groups, nil
}

// Correct asks the model to fix obvious ASR mistakes in each text and returns
// one corrected text per input, in order.
func (c *Client) Correct(ctx context.Context, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("没有可校对的语句")
	}
	content, err := c.complete(ctx, c.correctRequest(texts))
	if errors.Is(err, errNoChoices) || errors.Is(err, errTruncated) {
		return nil, invalidCorrection(err)
	}
	if err != nil {
		return nil, err
	}
	corrected, err := parseCorrections(content, len(texts))
	if err != nil {
		return nil, invalidCorrection(err)
	}
	return corrected, nil
}

func invalidGrouping(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidGrouping, err)
}

func invalidCorrection(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidCorrection, err)
}

// complete posts a chat request and returns the first choice content. It falls
// back to a schema-less retry when the server rejects response_format.
func (c *Client) complete(ctx context.Context, request chatRequest) (string, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode LM Studio request: %w", err)
	}
	raw, status, err := c.post(ctx, body)
	if status == http.StatusBadRequest && bytes.Contains(raw, []byte("response_format")) {
		request.ResponseFormat = nil
		body, err = json.Marshal(request)
		if err != nil {
			return "", fmt.Errorf("encode LM Studio request: %w", err)
		}
		raw, status, err = c.post(ctx, body)
	}
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("LM Studio returned %d", status)
	}
	var payload chatResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode LM Studio response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return "", errNoChoices
	}
	if payload.Choices[0].FinishReason == "length" {
		return "", errTruncated
	}
	return payload.Choices[0].Message.Content, nil
}

func (c *Client) post(ctx context.Context, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("LM Studio request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read LM Studio response: %w", err)
	}
	return raw, response.StatusCode, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	Temperature     float64         `json:"temperature"`
	MaxTokens       int             `json:"max_tokens"`
	Stream          bool            `json:"stream"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) chatRequest(texts []string) chatRequest {
	return chatRequest{
		Model:           c.model,
		Messages:        []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: BuildPrompt(texts)}},
		Temperature:     0,
		MaxTokens:       1024,
		Stream:          false,
		ReasoningEffort: "none",
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchema{
				Name:   "paragraphs",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"paragraphs": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"start": map[string]any{"type": "integer"},
									"end":   map[string]any{"type": "integer"},
								},
								"required":             []string{"start", "end"},
								"additionalProperties": false,
							},
						},
					},
					"required":             []string{"paragraphs"},
					"additionalProperties": false,
				},
			},
		},
	}
}

func BuildPrompt(texts []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 句：\n", len(texts))
	for i, text := range texts {
		line := strings.Join(strings.Fields(text), " ")
		if line == "" {
			line = "（无文字）"
		}
		fmt.Fprintf(&b, "%d. %s\n", i+1, line)
	}
	fmt.Fprintf(&b, "\n把连续句子合并为语义段落，完整覆盖 1..%d，不重叠。只输出 JSON。\n/no_think", len(texts))
	return b.String()
}

const correctionSystemPrompt = "你是中文语音转写（ASR）的校对助手。输入是语音识别结果，常含同音字、近音字错误。" +
	"修正每句中明显的识别错误，保持原意、用词和标点风格，不润色、不改写、不增删句子。" +
	"输出与输入等长的 JSON 数组，顺序一致。"

func (c *Client) correctRequest(texts []string) chatRequest {
	return chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "system", Content: correctionSystemPrompt}, {Role: "user", Content: BuildCorrectionPrompt(texts)}},
		Temperature: 0,
		// Thinking stays enabled: without reasoning the model is too conservative
		// to fix anything, and the budget must cover reasoning plus full texts.
		MaxTokens: 8192,
		Stream:    false,
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchema{
				Name:   "corrections",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"texts": map[string]any{
							"type":     "array",
							"items":    map[string]any{"type": "string"},
							"minItems": len(texts),
							"maxItems": len(texts),
						},
					},
					"required":             []string{"texts"},
					"additionalProperties": false,
				},
			},
		},
	}
}

// BuildCorrectionPrompt feeds the texts as a JSON array; a numbered list makes
// the model echo the numbering back into the corrected text.
func BuildCorrectionPrompt(texts []string) string {
	raw, err := json.Marshal(texts)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func parseCorrections(raw string, n int) ([]string, error) {
	text := extractJSON(raw)
	if text == "" {
		return nil, fmt.Errorf("LM Studio 返回空内容")
	}
	var payload struct {
		Texts []string `json:"texts"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("parse LM Studio JSON: %w", err)
	}
	if len(payload.Texts) != n {
		return nil, fmt.Errorf("校对结果 %d 句与输入 %d 句数量不一致", len(payload.Texts), n)
	}
	return payload.Texts, nil
}

var fencePattern = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*(.*?)\\s*```\\s*$")

func extractJSON(raw string) string {
	text := strings.TrimSpace(raw)
	if match := fencePattern.FindStringSubmatch(text); match != nil {
		text = strings.TrimSpace(match[1])
	} else if start, end := strings.Index(text, "{"), strings.LastIndex(text, "}"); start >= 0 && end > start {
		text = text[start : end+1]
	}
	return text
}

func parseGroups(raw string, n int) ([]Group, error) {
	text := extractJSON(raw)
	if text == "" {
		return nil, fmt.Errorf("LM Studio 返回空内容")
	}
	var payload struct {
		Paragraphs []struct {
			Start flexInt `json:"start"`
			End   flexInt `json:"end"`
		} `json:"paragraphs"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("parse LM Studio JSON: %w", err)
	}
	groups := make([]Group, 0, len(payload.Paragraphs))
	for _, item := range payload.Paragraphs {
		groups = append(groups, Group{Start: int(item.Start), End: int(item.End)})
	}
	groups, _, err := NormalizeGroups(groups, n)
	if err != nil {
		return nil, err
	}
	return groups, nil
}

type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	value := strings.TrimSpace(string(data))
	if len(value) >= 2 && value[0] == '"' {
		value = strings.Trim(value, `"`)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	if number != math.Trunc(number) {
		return fmt.Errorf("%q is not an integer", value)
	}
	*f = flexInt(int(number))
	return nil
}
