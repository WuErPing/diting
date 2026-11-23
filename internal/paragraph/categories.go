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

const categorySystemPrompt = "你是会议主题归类助手。输入是同一大时间段内已有的大主题及其小主题。" +
	"把含义相同或从属同一领域的大主题归并为少量稳定的领域级大类，内容确实不同的类别保持独立。" +
	"优先复用简洁清晰的已有名称，新名称不超过 8 字。每个输入类别必须恰好归入一个输出大类，只输出 JSON。"

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
		MaxTokens:       2048,
		Stream:          false,
		ReasoningEffort: "none",
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
	c.applyMiMoParams(&request, "disabled")
	content, err := c.complete(ctx, request)
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
	mapping := make(map[string]string, len(expected))
	for _, group := range payload.Categories {
		category := truncateTopicText(compactTopicText(group.Category), 8)
		if category == "" {
			return nil, fmt.Errorf("归并后的大主题为空")
		}
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
		byCategory[category] = append(byCategory[category], strings.TrimSpace(paragraph.TopicTitle()+" "+paragraph.TopicSummary()))
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
		log.Printf("Topic category consolidation failed; keeping original categories: categories=%d err=%v", len(input), err)
		return
	}
	for i := range paragraphs {
		if category, ok := mapping[paragraphs[i].Category]; ok {
			paragraphs[i].Category = category
		}
	}
}
