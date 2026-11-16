package tokusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Export structs mirror tokscale tui-data-cache.json schemaVersion 11.
// sourceBreakdown and models entries are [name, value] pairs, not objects.

type exportTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
}

func tokensOf(u Usage) exportTokens {
	return exportTokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning}
}

type exportPerformance struct {
	MsPer1KTokens   float64 `json:"msPer1KTokens"`
	TotalDurationMs int64   `json:"totalDurationMs"`
}

func performanceOf(u Usage) exportPerformance {
	p := exportPerformance{TotalDurationMs: u.DurationMs}
	if total := u.total(); total > 0 {
		p.MsPer1KTokens = float64(u.DurationMs) / (float64(total) / 1000)
	}
	return p
}

type exportBreakdownModel struct {
	Client      string            `json:"client"`
	Provider    string            `json:"provider"`
	DisplayName string            `json:"displayName"`
	Tokens      exportTokens      `json:"tokens"`
	Cost        float64           `json:"cost"`
	Messages    int64             `json:"messages"`
	Performance exportPerformance `json:"performance"`
}

type exportSourceBreakdown struct {
	Tokens exportTokens `json:"tokens"`
	Cost   float64      `json:"cost"`
	Models []any        `json:"models"`
}

type exportDaily struct {
	Date            string       `json:"date"`
	Tokens          exportTokens `json:"tokens"`
	Cost            float64      `json:"cost"`
	MessageCount    int64        `json:"messageCount"`
	TurnCount       int64        `json:"turnCount"`
	SourceBreakdown []any        `json:"sourceBreakdown"`
}

type exportHourlyModel struct {
	Provider string       `json:"provider"`
	Tokens   exportTokens `json:"tokens"`
	Cost     float64      `json:"cost"`
}

type exportHourly struct {
	Datetime     string       `json:"datetime"`
	Tokens       exportTokens `json:"tokens"`
	Cost         float64      `json:"cost"`
	Clients      []string     `json:"clients"`
	Models       []any        `json:"models"`
	MessageCount int64        `json:"messageCount"`
	TurnCount    int64        `json:"turnCount"`
}

type exportModel struct {
	Model        string            `json:"model"`
	Provider     string            `json:"provider"`
	Client       string            `json:"client"`
	Tokens       exportTokens      `json:"tokens"`
	Cost         float64           `json:"cost"`
	Performance  exportPerformance `json:"performance"`
	SessionCount int64             `json:"sessionCount"`
}

type exportData struct {
	Models        []exportModel  `json:"models"`
	Agents        []any          `json:"agents"`
	Daily         []exportDaily  `json:"daily"`
	Hourly        []exportHourly `json:"hourly"`
	TotalTokens   int64          `json:"totalTokens"`
	TotalCost     float64        `json:"totalCost"`
	CurrentStreak int64          `json:"currentStreak"`
	LongestStreak int64          `json:"longestStreak"`
}

type exportCache struct {
	SchemaVersion int64      `json:"schemaVersion"`
	Timestamp     int64      `json:"timestamp"`
	GroupBy       string     `json:"groupBy"`
	Data          exportData `json:"data"`
}

func (a *aggregate) buildCache(now time.Time) exportCache {
	dates := make([]string, 0, len(a.days))
	for date := range a.days {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	daily := make([]exportDaily, 0, len(dates))
	for _, date := range dates {
		day := a.days[date]
		clients := make([]string, 0, len(day.clients))
		for name := range day.clients {
			clients = append(clients, name)
		}
		sort.Strings(clients)
		breakdown := make([]any, 0, len(clients))
		for _, name := range clients {
			client := day.clients[name]
			breakdown = append(breakdown, []any{name, exportSourceBreakdown{
				Tokens: tokensOf(client.tokens),
				Models: breakdownModels(name, client.models),
			}})
		}
		daily = append(daily, exportDaily{
			Date:            date,
			Tokens:          tokensOf(day.tokens),
			MessageCount:    day.count,
			TurnCount:       day.count,
			SourceBreakdown: breakdown,
		})
	}

	hourKeys := make([]string, 0, len(a.hours))
	for key := range a.hours {
		hourKeys = append(hourKeys, key)
	}
	sort.Strings(hourKeys)
	hourly := make([]exportHourly, 0, len(hourKeys))
	for _, key := range hourKeys {
		h := a.hours[key]
		clients := make([]string, 0, len(h.clients))
		for name := range h.clients {
			clients = append(clients, name)
		}
		sort.Strings(clients)
		models := make([]any, 0, len(h.models))
		for _, mp := range sortedModelPairs(h.models) {
			m := h.models[mp]
			models = append(models, []any{mp[0], exportHourlyModel{Provider: mp[1], Tokens: tokensOf(m.tokens)}})
		}
		hourly = append(hourly, exportHourly{
			Datetime:     key,
			Tokens:       tokensOf(h.tokens),
			Clients:      clients,
			Models:       models,
			MessageCount: h.count,
			TurnCount:    h.count,
		})
	}

	keys := make([]modelKey, 0, len(a.models))
	for key := range a.models {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		ti, tj := a.models[keys[i]].tokens.total(), a.models[keys[j]].tokens.total()
		if ti != tj {
			return ti > tj
		}
		return keys[i].model < keys[j].model
	})
	models := make([]exportModel, 0, len(keys))
	for _, key := range keys {
		m := a.models[key]
		models = append(models, exportModel{
			Model:        key.model,
			Provider:     key.provider,
			Client:       key.client,
			Tokens:       tokensOf(m.tokens),
			Performance:  performanceOf(m.tokens),
			SessionCount: m.calls,
		})
	}

	current, longest := streaks(dates, now.Local())
	return exportCache{
		SchemaVersion: 11,
		Timestamp:     now.UnixMilli(),
		GroupBy:       "model",
		Data: exportData{
			Models:        models,
			Agents:        []any{},
			Daily:         daily,
			Hourly:        hourly,
			TotalTokens:   a.total.total(),
			CurrentStreak: current,
			LongestStreak: longest,
		},
	}
}

func breakdownModels(client string, models map[[2]string]*modelAgg) []any {
	pairs := make([]any, 0, len(models))
	for _, mp := range sortedModelPairs(models) {
		m := models[mp]
		pairs = append(pairs, []any{mp[0], exportBreakdownModel{
			Client:      client,
			Provider:    mp[1],
			DisplayName: mp[0],
			Tokens:      tokensOf(m.tokens),
			Messages:    m.calls,
			Performance: performanceOf(m.tokens),
		}})
	}
	return pairs
}

// sortedModelPairs orders {model, provider} keys by total tokens descending.
func sortedModelPairs(models map[[2]string]*modelAgg) [][2]string {
	keys := make([][2]string, 0, len(models))
	for key := range models {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		ti, tj := models[keys[i]].tokens.total(), models[keys[j]].tokens.total()
		if ti != tj {
			return ti > tj
		}
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}

// streaks counts consecutive-day runs over sorted dates; the current streak
// must reach today or yesterday.
func streaks(dates []string, now time.Time) (current, longest int64) {
	var run int64
	var prev time.Time
	for _, date := range dates {
		day, err := time.ParseInLocation("2006-01-02", date, now.Location())
		if err != nil {
			continue
		}
		if !prev.IsZero() && day.Equal(prev.AddDate(0, 0, 1)) {
			run++
		} else {
			run = 1
		}
		if run > longest {
			longest = run
		}
		prev = day
	}
	if !prev.IsZero() {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if prev.Equal(today) || prev.Equal(today.AddDate(0, 0, -1)) {
			current = run
		}
	}
	return current, longest
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	file, err := os.CreateTemp(dir, ".tui-data-cache-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace: %w", err)
	}
	return nil
}