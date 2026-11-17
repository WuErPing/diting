package paragraph

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrDaySummaryUnavailable = errors.New("按天会议纪要不可用")
	ErrDayNotFound           = errors.New("当天没有可总结的转写")
)

type DayMinutesResult struct {
	Date     string `json:"date"`
	Segments int    `json:"segments"`
	Markdown string `json:"markdown"`
}

type daySummarizer interface {
	SummarizeDay(ctx context.Context, date, text string) (string, error)
}

func (w *Worker) SummarizeDay(ctx context.Context, date string) (DayMinutesResult, error) {
	result := DayMinutesResult{Date: date}
	summarizer, ok := w.opts.Summarizer.(daySummarizer)
	if !ok || w.opts.Segments == nil {
		return result, ErrDaySummaryUnavailable
	}
	w.processMu.Lock()
	defer w.processMu.Unlock()

	segments, err := w.opts.Segments.All()
	if err != nil {
		return result, err
	}
	var transcript strings.Builder
	for _, segment := range segments {
		text := strings.TrimSpace(segment.Text)
		if segment.StartedAt.Local().Format("2006-01-02") != date || text == "" {
			continue
		}
		fmt.Fprintf(&transcript, "[%s] ", segment.StartedAt.Local().Format("15:04:05"))
		if segment.Speaker != "" {
			fmt.Fprintf(&transcript, "%s: ", w.speakerResolve(segment.Speaker))
		}
		transcript.WriteString(text)
		transcript.WriteByte('\n')
		result.Segments++
	}
	if result.Segments == 0 {
		return result, ErrDayNotFound
	}
	result.Markdown, err = summarizer.SummarizeDay(ctx, date, transcript.String())
	if err != nil {
		return DayMinutesResult{Date: date}, err
	}
	return result, nil
}

const dayMinutesSystemPrompt = "你是专业会议纪要助手。根据输入的某一天完整会议转写，整理为简洁、具体的中文 Markdown 会议纪要。" +
	"只输出 Markdown 正文，不要代码围栏。一级标题使用“# YYYY-MM-DD 会议纪要”并替换为输入日期；" +
	"二级标题固定为“会议概览”“关键讨论”“决策事项”“行动项”。行动项使用 `- [ ]` 任务列表，" +
	"已知时注明负责人和截止时间，未知时省略，不得编造。没有内容的章节写“无”。" +
	"保留关键结论、决定、待办、负责人和时间，不补充原文没有的信息，不评价。"

func (c *Client) SummarizeDay(ctx context.Context, date, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", ErrDayNotFound
	}
	request := chatRequest{
		Model: c.currentModel(),
		Messages: []chatMessage{
			{Role: "system", Content: dayMinutesSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("日期：%s\n\n%s", date, text)},
		},
		Temperature: 0,
		MaxTokens:   4096,
		Stream:      false,
	}
	c.applyMiMoParams(&request, "enabled")
	content, err := c.complete(ctx, request)
	if err != nil {
		return "", err
	}
	markdown := trimMarkdownFence(content)
	if markdown == "" {
		return "", fmt.Errorf("模型服务 返回空会议纪要")
	}
	return markdown, nil
}

var markdownFencePattern = regexp.MustCompile("(?s)^\\s*```(?:markdown|md)?\\s*(.*?)\\s*```\\s*$")

func trimMarkdownFence(content string) string {
	content = strings.TrimSpace(content)
	if match := markdownFencePattern.FindStringSubmatch(content); match != nil {
		return strings.TrimSpace(match[1])
	}
	return content
}
