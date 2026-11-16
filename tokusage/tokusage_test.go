package tokusage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 18, 9, 0, 0, 0, time.Local)

func testOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{
		LedgerPath: filepath.Join(dir, "tokusage.jsonl"),
		ExportPath: filepath.Join(dir, "tui-data-cache.json"),
		Logf:       func(string, ...any) {},
		Now:        func() time.Time { return testNow },
	}
}

func record(t *testing.T, r *Recorder, when time.Time, client, model string, u Usage) {
	t.Helper()
	if err := r.Record(Entry{Time: when, Client: client, Provider: "mimo", Model: model, Usage: u}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

func seedEntries(t *testing.T, r *Recorder) {
	t.Helper()
	day1 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.Local)
	record(t, r, day1, "diting", "flash", Usage{Input: 100, Output: 20, Reasoning: 5, DurationMs: 1000})
	record(t, r, day1.Add(30*time.Minute), "diting", "flash", Usage{Input: 50, Output: 10, CacheRead: 30, DurationMs: 500})
	record(t, r, day1.Add(time.Hour), "diting", "asr", Usage{Input: 200, Output: 40, DurationMs: 2000})
	record(t, r, testNow, "other", "flash", Usage{Input: 10, Output: 2})
}

type testTokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Reasoning  int64 `json:"reasoning"`
}

type testCache struct {
	SchemaVersion int64  `json:"schemaVersion"`
	Timestamp     int64  `json:"timestamp"`
	GroupBy       string `json:"groupBy"`
	Data          struct {
		Models []struct {
			Model        string     `json:"model"`
			Provider     string     `json:"provider"`
			Client       string     `json:"client"`
			Tokens       testTokens `json:"tokens"`
			SessionCount int64      `json:"sessionCount"`
			Performance  struct {
				MsPer1KTokens   float64 `json:"msPer1KTokens"`
				TotalDurationMs int64   `json:"totalDurationMs"`
			} `json:"performance"`
		} `json:"models"`
		Agents []any `json:"agents"`
		Daily  []struct {
			Date            string            `json:"date"`
			Tokens          testTokens        `json:"tokens"`
			MessageCount    int64             `json:"messageCount"`
			TurnCount       int64             `json:"turnCount"`
			SourceBreakdown []json.RawMessage `json:"sourceBreakdown"`
		} `json:"daily"`
		Hourly []struct {
			Datetime     string            `json:"datetime"`
			Tokens       testTokens        `json:"tokens"`
			Clients      []string          `json:"clients"`
			Models       []json.RawMessage `json:"models"`
			MessageCount int64             `json:"messageCount"`
		} `json:"hourly"`
		TotalTokens   int64 `json:"totalTokens"`
		CurrentStreak int64 `json:"currentStreak"`
		LongestStreak int64 `json:"longestStreak"`
	} `json:"data"`
}

func readCache(t *testing.T, path string) testCache {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	var c testCache
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	return c
}

// checkPair asserts a [name, value] pair array and returns its parts.
func checkPair(t *testing.T, raw json.RawMessage) (string, json.RawMessage) {
	t.Helper()
	var pair []json.RawMessage
	if err := json.Unmarshal(raw, &pair); err != nil {
		t.Fatalf("pair 不是数组: %v (%s)", err, raw)
	}
	if len(pair) != 2 {
		t.Fatalf("pair 长度应为 2，实际 %d", len(pair))
	}
	var name string
	if err := json.Unmarshal(pair[0], &name); err != nil {
		t.Fatalf("pair 首元素应为字符串: %v", err)
	}
	return name, pair[1]
}

func TestAggregateAndExport(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedEntries(t, r)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	c := readCache(t, opts.ExportPath)
	if c.SchemaVersion != 11 || c.GroupBy != "model" {
		t.Fatalf("schemaVersion/groupBy = %d/%s", c.SchemaVersion, c.GroupBy)
	}
	if c.Timestamp != testNow.UnixMilli() {
		t.Fatalf("timestamp = %d", c.Timestamp)
	}
	if len(c.Data.Daily) != 2 {
		t.Fatalf("daily 天数 = %d", len(c.Data.Daily))
	}
	d1 := c.Data.Daily[0]
	if d1.Date != "2026-11-17" || d1.MessageCount != 3 || d1.TurnCount != 3 {
		t.Fatalf("daily[0] = %+v", d1)
	}
	want := testTokens{Input: 350, Output: 70, CacheRead: 30, Reasoning: 5}
	if d1.Tokens != want {
		t.Fatalf("daily[0].tokens = %+v, want %+v", d1.Tokens, want)
	}
	if len(d1.SourceBreakdown) != 1 {
		t.Fatalf("sourceBreakdown 数 = %d", len(d1.SourceBreakdown))
	}
	name, value := checkPair(t, d1.SourceBreakdown[0])
	if name != "diting" {
		t.Fatalf("client = %s", name)
	}
	var sb struct {
		Tokens testTokens        `json:"tokens"`
		Models []json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(value, &sb); err != nil {
		t.Fatalf("sourceBreakdown value: %v", err)
	}
	if sb.Tokens != want || len(sb.Models) != 2 {
		t.Fatalf("sourceBreakdown = %+v", sb)
	}
	// models sorted by tokens desc: asr(240) > flash(215 within day1)
	modelName, modelValue := checkPair(t, sb.Models[0])
	if modelName != "asr" {
		t.Fatalf("day1 首个模型 = %s", modelName)
	}
	var bm struct {
		Client   string     `json:"client"`
		Provider string     `json:"provider"`
		Tokens   testTokens `json:"tokens"`
		Messages int64      `json:"messages"`
	}
	if err := json.Unmarshal(modelValue, &bm); err != nil {
		t.Fatalf("breakdown model: %v", err)
	}
	if bm.Client != "diting" || bm.Provider != "mimo" || bm.Messages != 1 || bm.Tokens.Input != 200 {
		t.Fatalf("breakdown model = %+v", bm)
	}

	if len(c.Data.Hourly) != 3 {
		t.Fatalf("hourly 数 = %d", len(c.Data.Hourly))
	}
	h0 := c.Data.Hourly[0]
	if h0.Datetime != "2026-11-17 10:00:00" || h0.MessageCount != 2 || len(h0.Models) != 1 || len(h0.Clients) != 1 || h0.Clients[0] != "diting" {
		t.Fatalf("hourly[0] = %+v", h0)
	}
	checkPair(t, h0.Models[0])

	if len(c.Data.Models) != 3 {
		t.Fatalf("models 数 = %d", len(c.Data.Models))
	}
	flash := c.Data.Models[1]
	if flash.Model != "flash" || flash.Client != "diting" || flash.SessionCount != 2 || flash.Tokens.Input != 150 || flash.Performance.TotalDurationMs != 1500 {
		t.Fatalf("flash = %+v", flash)
	}
	if c.Data.TotalTokens != 467 {
		t.Fatalf("totalTokens = %d", c.Data.TotalTokens)
	}
	if c.Data.CurrentStreak != 2 || c.Data.LongestStreak != 2 {
		t.Fatalf("streak = %d/%d", c.Data.CurrentStreak, c.Data.LongestStreak)
	}
	if c.Data.Agents == nil {
		t.Fatal("agents 应为空数组而非 null")
	}
}

func TestLedgerRecovery(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedEntries(t, r)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	before, err := os.ReadFile(opts.ExportPath)
	if err != nil {
		t.Fatal(err)
	}

	r2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r2.Close()
	if err := r2.Export(); err != nil {
		t.Fatalf("Export: %v", err)
	}
	after, err := os.ReadFile(opts.ExportPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("恢复后导出与原始导出不一致")
	}
}

func TestCorruptLedgerTailSkipped(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedEntries(t, r)
	r.Close()

	f, err := os.OpenFile(opts.LedgerPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":1,"time":`) // 崩溃产生的半行
	f.Close()

	warned := false
	opts.Logf = func(format string, args ...any) { warned = true }
	r2, err := Open(opts)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r2.Close()
	if !warned {
		t.Fatal("坏行应产生警告")
	}
	record(t, r2, testNow, "diting", "flash", Usage{Input: 1, Output: 1})
	c := readCache(t, opts.ExportPath)
	if c.Data.TotalTokens != 469 {
		t.Fatalf("totalTokens = %d, want 469", c.Data.TotalTokens)
	}
}

func TestEmptyNoExport(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	if err := r.Export(); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := os.Stat(opts.ExportPath); !os.IsNotExist(err) {
		t.Fatalf("空账本不应导出: %v", err)
	}
}

func TestAtomicWriteNoTempLeft(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedEntries(t, r)
	r.Close()
	entries, err := os.ReadDir(filepath.Dir(opts.ExportPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tui-data-cache-") {
			t.Fatalf("temp 文件残留: %s", e.Name())
		}
	}
}

func TestRecordAfterClose(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r.Close()
	if err := r.Record(Entry{Client: "x", Model: "m"}); err == nil {
		t.Fatal("关闭后 Record 应报错")
	}
}

func TestConcurrentRecord(t *testing.T) {
	opts := testOptions(t)
	r, err := Open(opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := r.Record(Entry{
					Time:     testNow,
					Client:   fmt.Sprintf("client-%d", g),
					Provider: "mimo",
					Model:    "flash",
					Usage:    Usage{Input: 1, Output: 1},
				}); err != nil {
					t.Errorf("Record: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	r.Close()
	c := readCache(t, opts.ExportPath)
	if c.Data.TotalTokens != 160 {
		t.Fatalf("totalTokens = %d, want 160", c.Data.TotalTokens)
	}
	if len(c.Data.Daily) != 1 || c.Data.Daily[0].MessageCount != 80 {
		t.Fatalf("daily = %+v", c.Data.Daily)
	}
}