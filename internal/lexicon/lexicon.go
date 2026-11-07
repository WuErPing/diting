package lexicon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	Version      = 1
	maxRules     = 200
	maxFromRunes = 20
	maxToRunes   = 40
)

var ErrInvalidRule = errors.New("术语规则无效")

type Rule struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type lexiconFile struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

type Lexicon struct {
	mu       sync.RWMutex
	path     string
	rules    []Rule
	replacer *strings.Replacer
}

func Open(path string) (*Lexicon, error) {
	instance := &Lexicon{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return instance, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lexicon %s: %w", path, err)
	}
	var file lexiconFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse lexicon %s: %w", path, err)
	}
	rules, err := normalize(file.Rules)
	if err != nil {
		return nil, fmt.Errorf("lexicon %s: %w", path, err)
	}
	instance.setRules(rules)
	return instance, nil
}

// Apply replaces known terms in one pass. A nil Lexicon is a no-op.
func (l *Lexicon) Apply(text string) string {
	if l == nil || text == "" {
		return text
	}
	l.mu.RLock()
	replacer := l.replacer
	l.mu.RUnlock()
	if replacer == nil {
		return text
	}
	return replacer.Replace(text)
}

func (l *Lexicon) Rules() []Rule {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]Rule{}, l.rules...)
}

func (l *Lexicon) Replace(rules []Rule) error {
	normalized, err := normalize(rules)
	if err != nil {
		return err
	}
	if err := l.write(normalized); err != nil {
		return err
	}
	l.setRules(normalized)
	return nil
}

func Validate(rules []Rule) error {
	_, err := normalize(rules)
	return err
}

func normalize(rules []Rule) ([]Rule, error) {
	if len(rules) > maxRules {
		return nil, fmt.Errorf("%w: 最多 %d 条", ErrInvalidRule, maxRules)
	}
	seen := make(map[string]struct{}, len(rules))
	normalized := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		from := strings.TrimSpace(rule.From)
		to := strings.TrimSpace(rule.To)
		if from == "" {
			return nil, fmt.Errorf("%w: 错词不能为空", ErrInvalidRule)
		}
		if utf8.RuneCountInString(from) > maxFromRunes {
			return nil, fmt.Errorf("%w: 错词不能超过 %d 个字", ErrInvalidRule, maxFromRunes)
		}
		if utf8.RuneCountInString(to) > maxToRunes {
			return nil, fmt.Errorf("%w: 正词不能超过 %d 个字", ErrInvalidRule, maxToRunes)
		}
		if from == to {
			return nil, fmt.Errorf("%w: 错词和正词不能相同", ErrInvalidRule)
		}
		if hasControl(from) || hasControl(to) {
			return nil, fmt.Errorf("%w: 不能包含控制字符", ErrInvalidRule)
		}
		if _, ok := seen[from]; ok {
			return nil, fmt.Errorf("%w: 错词 %q 重复", ErrInvalidRule, from)
		}
		seen[from] = struct{}{}
		normalized = append(normalized, Rule{From: from, To: to})
	}
	return normalized, nil
}

func hasControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func (l *Lexicon) setRules(rules []Rule) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rules = append([]Rule(nil), rules...)
	if len(rules) == 0 {
		l.replacer = nil
		return
	}
	sorted := append([]Rule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return utf8.RuneCountInString(sorted[i].From) > utf8.RuneCountInString(sorted[j].From)
	})
	pairs := make([]string, 0, len(sorted)*2)
	for _, rule := range sorted {
		pairs = append(pairs, rule.From, rule.To)
	}
	l.replacer = strings.NewReplacer(pairs...)
}

func (l *Lexicon) write(rules []Rule) error {
	if l.path == "" {
		return nil
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create lexicon directory: %w", err)
	}
	data, err := json.MarshalIndent(lexiconFile{Version: Version, Rules: rules}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode lexicon: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(dir, ".lexicon-*.json")
	if err != nil {
		return fmt.Errorf("create lexicon temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write lexicon: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync lexicon: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close lexicon: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace lexicon: %w", err)
	}
	return nil
}
