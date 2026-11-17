package tokusage

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ledgerRecord is the on-disk JSONL line format (version 1).
type ledgerRecord struct {
	V          int       `json:"v"`
	Time       time.Time `json:"time"`
	Client     string    `json:"client"`
	Provider   string    `json:"provider"`
	Model      string    `json:"model"`
	Input      int64     `json:"input"`
	Output     int64     `json:"output"`
	CacheRead  int64     `json:"cacheRead"`
	CacheWrite int64     `json:"cacheWrite"`
	Reasoning  int64     `json:"reasoning"`
	DurationMs int64     `json:"durationMs"`
}

type ledger struct {
	file *os.File
	enc  *json.Encoder
}

// openLedger replays existing lines into add, then opens the file for append.
// Corrupt lines (e.g. a half-written tail after a crash) are logged and skipped.
func openLedger(path string, logf func(string, ...any), add func(Entry)) (*ledger, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("tokusage: 创建账本目录: %w", err)
	}
	if existing, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(existing)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			var rec ledgerRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil || rec.V != 1 {
				logf("tokusage: 跳过账本 %s 第 %d 行（损坏或版本不符）", path, lineNo)
				continue
			}
			add(Entry{
				Time:     rec.Time,
				Client:   rec.Client,
				Provider: rec.Provider,
				Model:    rec.Model,
				Usage: Usage{
					Input:      rec.Input,
					Output:     rec.Output,
					CacheRead:  rec.CacheRead,
					CacheWrite: rec.CacheWrite,
					Reasoning:  rec.Reasoning,
					DurationMs: rec.DurationMs,
				},
			})
		}
		if err := scanner.Err(); err != nil {
			existing.Close()
			return nil, fmt.Errorf("tokusage: 读取账本 %s: %w", path, err)
		}
		existing.Close()
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("tokusage: 打开账本 %s: %w", path, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("tokusage: 打开账本 %s: %w", path, err)
	}
	return &ledger{file: file, enc: json.NewEncoder(file)}, nil
}

func (l *ledger) append(e Entry) error {
	rec := ledgerRecord{
		V:          1,
		Time:       e.Time,
		Client:     e.Client,
		Provider:   e.Provider,
		Model:      e.Model,
		Input:      e.Usage.Input,
		Output:     e.Usage.Output,
		CacheRead:  e.Usage.CacheRead,
		CacheWrite: e.Usage.CacheWrite,
		Reasoning:  e.Usage.Reasoning,
		DurationMs: e.Usage.DurationMs,
	}
	if err := l.enc.Encode(rec); err != nil {
		return fmt.Errorf("tokusage: 写账本: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("tokusage: sync 账本: %w", err)
	}
	return nil
}

func (l *ledger) close() error {
	return l.file.Close()
}
