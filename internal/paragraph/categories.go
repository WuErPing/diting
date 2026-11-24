package paragraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

type CategoryTopics struct {
	Category string   `json:"category"`
	Topics   []string `json:"topics"`
}

type categoryCanonicalizer interface {
	CanonicalizeCategories(ctx context.Context, categories []CategoryTopics) (map[string]string, error)
}

const categorySystemPrompt = "你是会议主题归类助手。输入是同一大时间段内已有的大主题，以及按时间顺序排列的小主题、摘要和原文摘录。" +
	"每个大时间段只保留一个大主题，具体差异由小主题承载。时间段内的全部输入类别必须归并到这一个大类，不得再按领域、事件、人物、资产或短暂插话拆分。" +
	"先结合时间顺序和上下文还原完整讨论，输出一个能概括该时段核心讨论且不超过 8 字的名称，不要只选用只能覆盖部分内容的领域词。" +
	"例如，英伟达 CEO 回归及其股价、期权、公司管理、半导体和加密资产行情都在同一时间段时，统一为“黄仁勋回归”，不要保留“金融投资”“金融市场”“公司治理”“加密货币行情”等多个大类。" +
	"每个输入类别的名称必须恰好出现在该唯一大类的 sources 中一次，输出的 categories 数组只能有一个类别，只输出 JSON。"

func (c *Client) CanonicalizeCategories(ctx context.Context, categories []CategoryTopics) (map[string]string, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("没有可归并的主题类别")
	}
	input, err := json.Marshal(categories)
	if err != nil {
		return nil, err
	}
	request := chatRequest{
		Model: c.currentModel(),
		Messages: []chatMessage{
			{Role: "system", Content: categorySystemPrompt},
			{Role: "user", Content: string(input)},
		},
		Temperature:     0,
		MaxTokens:       4096,
		Stream:          false,
		ReasoningEffort: "low",
		ResponseFormat: &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchema{
				Name:   "categories",
				Strict: true,
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"categories": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"category": map[string]any{"type": "string"},
									"sources": map[string]any{
										"type":     "array",
										"items":    map[string]any{"type": "string"},
										"minItems": 1,
									},
								},
								"required":             []string{"category", "sources"},
								"additionalProperties": false,
							},
						},
					},
					"required":             []string{"categories"},
					"additionalProperties": false,
				},
			},
		},
	}
	c.applyMiMoParams(&request, "enabled")
	content, err := c.completeWithClient(ctx, request, c.categoryHTTP)
	if errors.Is(err, errNoChoices) || errors.Is(err, errTruncated) {
		return nil, invalidGrouping(err)
	}
	if err != nil {
		return nil, err
	}
	expected := make([]string, len(categories))
	for i, category := range categories {
		expected[i] = category.Category
	}
	mapping, err := parseCategoryMapping(content, expected)
	if err != nil {
		return nil, invalidGrouping(err)
	}
	return mapping, nil
}

func parseCategoryMapping(raw string, expected []string) (map[string]string, error) {
	var payload struct {
		Categories []struct {
			Category string   `json:"category"`
			Sources  []string `json:"sources"`
		} `json:"categories"`
	}
	if err := json.Unmarshal([]byte(extractJSON(raw)), &payload); err != nil {
		return nil, fmt.Errorf("parse 模型服务 JSON: %w", err)
	}
	remaining := make(map[string]struct{}, len(expected))
	for _, category := range expected {
		remaining[category] = struct{}{}
	}
	if len(payload.Categories) == 0 {
		return nil, fmt.Errorf("归并结果没有大主题")
	}
	category := truncateTopicText(compactTopicText(payload.Categories[0].Category), 8)
	if category == "" {
		return nil, fmt.Errorf("归并后的大主题为空")
	}
	mapping := make(map[string]string, len(expected))
	for _, group := range payload.Categories {
		for _, source := range group.Sources {
			if _, ok := remaining[source]; !ok {
				return nil, fmt.Errorf("类别 %q 重复或不在输入中", source)
			}
			mapping[source] = category
			delete(remaining, source)
		}
	}
	if len(remaining) > 0 {
		return nil, fmt.Errorf("归并结果未覆盖全部 %d 个类别", len(expected))
	}
	return mapping, nil
}

func canonicalizeTimePeriods(ctx context.Context, paragraphs []Paragraph, grouper Grouper) {
	canonicalizer, ok := grouper.(categoryCanonicalizer)
	if !ok {
		return
	}
	for start := 0; start < len(paragraphs); {
		end := start + 1
		for end < len(paragraphs) {
			if paragraphs[end].StartedAt.Sub(paragraphs[end-1].EndedAt) >= defaultMaxSilence {
				break
			}
			end++
		}
		canonicalizeCategoryPeriod(ctx, paragraphs[start:end], canonicalizer)
		start = end
	}
}

func canonicalizeCategoryPeriod(ctx context.Context, paragraphs []Paragraph, canonicalizer categoryCanonicalizer) {
	if ctx.Err() != nil {
		return
	}
	order := make([]string, 0)
	byCategory := make(map[string][]string)
	for _, paragraph := range paragraphs {
		category := compactTopicText(paragraph.Category)
		if !paragraph.HasTopic() || category == "" {
			continue
		}
		if _, ok := byCategory[category]; !ok {
			order = append(order, category)
		}
		topic := strings.TrimSpace(paragraph.TopicTitle() + " " + paragraph.TopicSummary())
		excerpt := truncateTopicText(strings.Join(strings.Fields(paragraph.Text), " "), 160)
		context := fmt.Sprintf("%s %s 原文：%s", paragraph.StartedAt.Local().Format("15:04:05"), topic, excerpt)
		byCategory[category] = append(byCategory[category], context)
	}
	if len(order) < 2 {
		return
	}
	input := make([]CategoryTopics, len(order))
	for i, category := range order {
		input[i] = CategoryTopics{Category: category, Topics: byCategory[category]}
	}
	var mapping map[string]string
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		mapping, err = canonicalizer.CanonicalizeCategories(ctx, input)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		log.Printf("Topic category consolidation failed; using first category for the time period: categories=%d err=%v", len(input), err)
		mapping = make(map[string]string, len(order))
		for _, category := range order {
			mapping[category] = order[0]
		}
	}
	for i := range paragraphs {
		if category, ok := mapping[paragraphs[i].Category]; ok {
			paragraphs[i].Category = category
		}
	}
}
