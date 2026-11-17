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
	"sync"
	"time"
)

const (
	DefaultBaseURL = "https://token-plan-cn.xiaomimimo.com/v1"
	DefaultModel   = "mimo-v2.6-flash"
)

// ErrInvalidGrouping marks a fast, malformed model reply. It must not trigger
// the unavailability cooldown, which exists for hanging or unreachable servers.
var ErrInvalidGrouping = errors.New("invalid model grouping")

// ErrInvalidCorrection marks a malformed correction reply; like ErrInvalidGrouping
// it must not trigger the unavailability cooldown.
var ErrInvalidCorrection = errors.New("模型语义修正无效")

var (
	errNoChoices = errors.New("model API returned no choices")
	errTruncated = errors.New("model API response truncated (finish_reason=length)")
)

const (
	maxResponseBytes  = 1 << 20
	requestTimeout    = 90 * time.Second
	modelCheckTimeout = 15 * time.Second
)

// correctionChunkRunes bounds the text sent per correction request. Thinking
// replies use roughly 8 reasoning tokens per input rune, so ~500 runes keeps
// reasoning (~4k) plus output well under the 8192 max_tokens and avoids the
// truncation that discards corrections on long runs.
const correctionChunkRunes = 500

const systemPrompt = "你是会议议题分析助手。输入是带说话人标签的连续转写，说话人变化不代表议题变化。" +
	"把围绕同一问题、目标或决策的连续发言归为同一会议议题，再为议题生成具体名称和简明介绍。" +
	"只有包含实质观点、问题、事实、方案、决策、分歧、任务或行动项的段落才标记 valuable=true；" +
	"寒暄、闲聊、语气词、重复确认和无信息过渡标记 valuable=false，不要为了覆盖所有语句而制造主题。" +
	"必须完整覆盖 1..N、连续且无重叠；不新增、删除或改写原文；只输出 JSON。"

type ClientConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
	// OnUsage, when set, receives the raw usage object of each successful
	// API response (including truncated ones, which still consume tokens).
	OnUsage func(model string, usage json.RawMessage, durationMs int64)
}

type Client struct {
	mu      sync.RWMutex
	baseURL string
	apiKey  string
	model   string
	mimo    bool
	http    *http.Client
	onUsage func(model string, usage json.RawMessage, durationMs int64)
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
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		mimo:    isMiMoURL(cfg.BaseURL),
		http:    &http.Client{Timeout: cfg.Timeout},
		onUsage: cfg.OnUsage,
	}
}

func isMiMoURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "xiaomimimo.com" || strings.HasSuffix(host, ".xiaomimimo.com")
}

func (c *Client) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// SetModel switches the model used for subsequent Group and Correct requests.
func (c *Client) SetModel(model string) {
	c.mu.Lock()
	c.model = model
	c.mu.Unlock()
}

func (c *Client) currentModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// ValidateBaseURL allows local HTTP endpoints and requires HTTPS when transcript
// text is sent to a remote model API.
func ValidateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid model API URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("model API URL must use http or https, got %q", parsed.Scheme)
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	loopback := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if parsed.Scheme == "http" && !loopback {
		return fmt.Errorf("remote model API URL must use HTTPS, got %q", host)
	}
	if host == "" {
		return fmt.Errorf("model API URL must include a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("model API URL must not contain credentials")
	}
	return nil
}

func (c *Client) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, modelCheckTimeout)
	defer cancel()
	models, err := c.listModels(ctx)
	if err != nil {
		return err
	}
	current := c.currentModel()
	for _, model := range models {
		if model == current {
			return nil
		}
	}
	return fmt.Errorf("模型服务 未找到模型 %q", current)
}

// Models lists the model IDs 模型服务 currently serves.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.listModels(ctx)
}

func (c *Client) listModels(ctx context.Context) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.authenticate(request)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
		return nil, modelAPIError(response.StatusCode, raw)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode 模型服务 /models: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		models = append(models, model.ID)
	}
	return models, nil
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

const summarySystemPrompt = "你是会议纪要助手。把输入的一段会议转写总结为简洁、具体的中文摘要。" +
	"保留关键结论、决定、待办和负责人，不补充原文没有的信息，不评价，不使用标题或列表。只输出一段总结。"

func (c *Client) Summarize(ctx context.Context, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("没有可总结的内容")
	}
	request := chatRequest{
		Model:       c.currentModel(),
		Messages:    []chatMessage{{Role: "system", Content: summarySystemPrompt}, {Role: "user", Content: text}},
		Temperature: 0,
		MaxTokens:   2048,
		Stream:      false,
	}
	c.applyMiMoParams(&request, "enabled")
	content, err := c.complete(ctx, request)
	if err != nil {
		return "", err
	}
	summary := strings.TrimSpace(content)
	if summary == "" {
		return "", fmt.Errorf("模型服务 返回空总结")
	}
	return summary, nil
}

// Correct asks the model to fix obvious ASR mistakes in each text and returns
// one corrected text per input, in order.
//
// Long inputs are decomposed into sentence-aligned units and sent in bounded
// chunks. Thinking-enabled replies spend most of the token budget on reasoning
// (measured ~8 reasoning tokens per input character), so a single large request
// gets truncated and the whole correction is discarded. Chunking keeps every
// request small enough to finish; a chunk that still fails is left unchanged
// rather than failing the entire batch.
func (c *Client) Correct(ctx context.Context, texts []string) ([]string, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("没有可校对的语句")
	}
	pieces := make([][]string, len(texts))
	var units []string
	var owners []unitOwner
	for i, text := range texts {
		pieces[i] = correctionUnits(text, correctionChunkRunes)
		for j := range pieces[i] {
			units = append(units, pieces[i][j])
			owners = append(owners, unitOwner{parent: i, index: j})
		}
	}
	if len(units) == 0 {
		return append([]string(nil), texts...), nil
	}

	chunks := chunkRanges(units, correctionChunkRunes)
	var lastInvalid error
	invalid := 0
	for _, chunk := range chunks {
		corrected, err := c.correctChunk(ctx, units[chunk.start:chunk.end])
		if err != nil {
			if errors.Is(err, ErrInvalidCorrection) {
				lastInvalid = err
				invalid++
				continue
			}
			return nil, err
		}
		for k, owner := range owners[chunk.start:chunk.end] {
			pieces[owner.parent][owner.index] = corrected[k]
		}
	}
	// Only a wholly-failed batch is reported invalid; partial success keeps the
	// corrected chunks and the originals for the rest.
	if invalid == len(chunks) {
		return nil, lastInvalid
	}
	result := make([]string, len(texts))
	for i := range texts {
		result[i] = strings.Join(pieces[i], "")
	}
	return result, nil
}

// correctChunk sends one bounded batch and returns one text per input.
func (c *Client) correctChunk(ctx context.Context, texts []string) ([]string, error) {
	callCtx, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	content, err := c.complete(callCtx, c.correctRequest(texts))
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

type unitOwner struct {
	parent int
	index  int
}

// sentenceDelimiters end a correction unit; keeping the delimiter attached to
// the preceding sentence makes the units rejoin into the exact original text.
const sentenceDelimiters = "。！？；!?;\n"

// correctionUnits returns the text whole when it fits the budget, otherwise
// splits it into sentences. An empty text yields no units.
func correctionUnits(text string, budget int) []string {
	if text == "" {
		return nil
	}
	if len([]rune(text)) <= budget {
		return []string{text}
	}
	return splitSentences(text)
}

func splitSentences(text string) []string {
	var units []string
	var b strings.Builder
	for _, r := range text {
		b.WriteRune(r)
		if strings.ContainsRune(sentenceDelimiters, r) {
			units = append(units, b.String())
			b.Reset()
		}
	}
	if b.Len() > 0 {
		units = append(units, b.String())
	}
	return units
}

// chunkRanges packs units into index ranges whose total rune count stays within
// budget. A single unit larger than budget forms its own chunk.
func chunkRanges(units []string, budget int) []chunkRange {
	var ranges []chunkRange
	start := 0
	for start < len(units) {
		end := start + 1
		runes := len([]rune(units[start]))
		for end < len(units) {
			next := len([]rune(units[end]))
			if runes+next > budget {
				break
			}
			runes += next
			end++
		}
		ranges = append(ranges, chunkRange{start: start, end: end})
		start = end
	}
	return ranges
}

type chunkRange struct {
	start int
	end   int
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
	started := time.Now()
	body, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode 模型服务 request: %w", err)
	}
	raw, status, err := c.post(ctx, body)
	if status == http.StatusBadRequest && bytes.Contains(raw, []byte("response_format")) {
		request.ResponseFormat = nil
		body, err = json.Marshal(request)
		if err != nil {
			return "", fmt.Errorf("encode 模型服务 request: %w", err)
		}
		raw, status, err = c.post(ctx, body)
	}
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", modelAPIError(status, raw)
	}
	var payload chatResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", fmt.Errorf("decode 模型服务 response: %w", err)
	}
	if c.onUsage != nil && len(payload.Usage) > 0 {
		c.onUsage(request.Model, payload.Usage, time.Since(started).Milliseconds())
	}
	if len(payload.Choices) == 0 {
		return "", errNoChoices
	}
	if payload.Choices[0].FinishReason == "length" {
		return "", errTruncated
	}
	return payload.Choices[0].Message.Content, nil
}

func modelAPIError(status int, raw []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &payload)
	message := payload.Error.Message
	if message == "" {
		message = payload.Message
	}
	if message == "" {
		return fmt.Errorf("模型服务 returned %d", status)
	}
	return fmt.Errorf("模型服务 returned %d: %s", status, message)
}

func (c *Client) post(ctx context.Context, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	c.authenticate(request)
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("模型服务 request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read 模型服务 response: %w", err)
	}
	return raw, response.StatusCode, nil
}

func (c *Client) authenticate(request *http.Request) {
	if c.apiKey == "" {
		return
	}
	request.Header.Set("api-key", c.apiKey)
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
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

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Temperature         float64         `json:"temperature"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Stream              bool            `json:"stream"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	Thinking            *thinkingConfig `json:"thinking,omitempty"`
	ResponseFormat      *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage"`
}

func (c *Client) chatRequest(texts []string) chatRequest {
	request := chatRequest{
		Model:           c.currentModel(),
		Messages:        []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: BuildPrompt(texts)}},
		Temperature:     0,
		MaxTokens:       2048,
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
									"start":    map[string]any{"type": "integer"},
									"end":      map[string]any{"type": "integer"},
									"title":    map[string]any{"type": "string"},
									"summary":  map[string]any{"type": "string"},
									"valuable": map[string]any{"type": "boolean"},
								},
								"required":             []string{"start", "end", "title", "summary", "valuable"},
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
	c.applyMiMoParams(&request, "disabled")
	return request
}

func (c *Client) applyMiMoParams(request *chatRequest, thinking string) {
	if !c.mimo {
		return
	}
	request.MaxCompletionTokens = request.MaxTokens
	request.MaxTokens = 0
	request.ReasoningEffort = ""
	request.Thinking = &thinkingConfig{Type: thinking}
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
	fmt.Fprintf(&b, "\n按会议议题切分连续发言，完整覆盖 1..%d，不重叠。同一议题可以包含多人的问答、讨论、决策和行动项；不要按说话人切分。"+
		"每段提供不超过 12 字的具体议题名称 title、不超过 50 字且包含结论或待办的简明介绍 summary，以及布尔值 valuable。"+
		"有实质讨论价值时 valuable=true；寒暄、闲聊、语气词、重复确认和无信息过渡时 valuable=false，title 和 summary 留空。只输出 JSON。\n/no_think", len(texts))
	return b.String()
}

const correctionSystemPrompt = "你是中文语音转写（ASR）的校对助手。输入是语音识别结果，常含同音字、近音字错误。" +
	"修正每句中明显的识别错误，保持原意、用词和标点风格，不润色、不改写、不增删句子。" +
	"输出与输入等长的 JSON 数组，顺序一致。"

func (c *Client) correctRequest(texts []string) chatRequest {
	request := chatRequest{
		Model:       c.currentModel(),
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
	c.applyMiMoParams(&request, "enabled")
	return request
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
		return nil, fmt.Errorf("模型服务 返回空内容")
	}
	var payload struct {
		Texts []string `json:"texts"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("parse 模型服务 JSON: %w", err)
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
		return nil, fmt.Errorf("模型服务 返回空内容")
	}
	var payload struct {
		Paragraphs []struct {
			Start    flexInt `json:"start"`
			End      flexInt `json:"end"`
			Title    string  `json:"title"`
			Summary  string  `json:"summary"`
			Valuable *bool   `json:"valuable"`
		} `json:"paragraphs"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, fmt.Errorf("parse 模型服务 JSON: %w", err)
	}
	groups := make([]Group, 0, len(payload.Paragraphs))
	for _, item := range payload.Paragraphs {
		groups = append(groups, Group{
			Start: int(item.Start), End: int(item.End), Title: item.Title, Summary: item.Summary,
			TopicExcluded: item.Valuable != nil && !*item.Valuable,
		})
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
