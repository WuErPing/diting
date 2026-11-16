package paragraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

const (
	settingsVersion  = 1
	maxModelRunes    = 200
	ASRProviderLocal = "local"
	ASRProviderMiMo  = "mimo"
)

// ErrInvalidSettings marks a rejected settings update; it must surface as a 400
// rather than tripping the 模型服务 unavailability cooldown.
var ErrInvalidSettings = errors.New("设置无效")

// Snapshot is the runtime-controllable model state: paragraph splitting and
// correction, plus the final ASR provider.
type Snapshot struct {
	Model   string `json:"model"`
	Correct bool   `json:"correct"`
	ASR     string `json:"asr"`
}

type settingsFile struct {
	Version int    `json:"version"`
	Model   string `json:"model"`
	Correct bool   `json:"correct"`
	ASR     string `json:"asr"`
}

type ASRController interface {
	SetASRProvider(provider string) error
}

// Settings coordinates the Client, Worker and ASR controller so the web UI can
// switch models at runtime, persisting the choices across restarts. It mirrors
// lexicon.Lexicon's atomic write so a crash cannot corrupt the file.
type Settings struct {
	mu            sync.RWMutex
	path          string
	model         string
	correct       bool
	asr           string
	asrController ASRController
	client        *Client
	worker        *Worker
	logf          func(format string, args ...any)
}

// OpenSettings loads data/settings.json (falling back to defaults when absent)
// and applies the result to the client and worker. Once the file exists it is
// the source of truth, so the CLI flags only seed the first run.
func OpenSettings(path string, client *Client, worker *Worker, defaults Snapshot, logf func(format string, args ...any), controllers ...ASRController) (*Settings, error) {
	if logf == nil {
		logf = log.Printf
	}
	if defaults.ASR == "" {
		defaults.ASR = ASRProviderLocal
	}
	s := &Settings{
		path:    path,
		model:   defaults.Model,
		correct: defaults.Correct,
		asr:     defaults.ASR,
		client:  client,
		worker:  worker,
		logf:    logf,
	}
	if len(controllers) > 0 {
		s.asrController = controllers[0]
	}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Keep the defaults seeded from the CLI flags.
	case err != nil:
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	default:
		var file settingsFile
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parse settings %s: %w", path, err)
		}
		if model := strings.TrimSpace(file.Model); model != "" {
			s.model = model
		}
		s.correct = file.Correct
		if file.ASR == ASRProviderLocal || file.ASR == ASRProviderMiMo {
			s.asr = file.ASR
		}
	}
	if strings.TrimSpace(s.model) == "" {
		s.model = DefaultModel
	}
	s.apply()
	return s, nil
}

func (s *Settings) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{Model: s.model, Correct: s.correct, ASR: s.asr}
}

// Update validates the new state, persists it atomically, then applies it to the
// client, worker and ASR controller. A failed write leaves the running state unchanged.
func (s *Settings) Update(model string, correct bool, providers ...string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("%w: 模型不能为空", ErrInvalidSettings)
	}
	if utf8.RuneCountInString(model) > maxModelRunes {
		return fmt.Errorf("%w: 模型名不能超过 %d 个字符", ErrInvalidSettings, maxModelRunes)
	}
	if hasControlChars(model) {
		return fmt.Errorf("%w: 模型名不能包含控制字符", ErrInvalidSettings)
	}
	s.mu.RLock()
	provider := s.asr
	controller := s.asrController
	s.mu.RUnlock()
	if len(providers) > 0 {
		provider = providers[0]
	}
	if provider != ASRProviderLocal && provider != ASRProviderMiMo {
		return fmt.Errorf("%w: 语音识别只能是 local 或 mimo", ErrInvalidSettings)
	}
	if provider == ASRProviderMiMo && controller == nil {
		return fmt.Errorf("%w: MiMo ASR 未配置", ErrInvalidSettings)
	}
	if err := s.write(model, correct, provider); err != nil {
		return err
	}
	s.mu.Lock()
	s.model = model
	s.correct = correct
	s.asr = provider
	s.mu.Unlock()
	s.apply()
	s.logf("模型设置已更新: 模型=%s 语义修正=%v 语音识别=%s", model, correct, provider)
	return nil
}

// AvailableModels lists the models 模型服务 currently serves.
func (s *Settings) AvailableModels(ctx context.Context) ([]string, error) {
	if s.client == nil {
		return nil, errors.New("模型服务 客户端未启用")
	}
	return s.client.Models(ctx)
}

// CorrectRecent runs an explicit correction even when automatic correction is paused.
func (s *Settings) CorrectRecent(ctx context.Context, limit int) (CorrectionResult, error) {
	if s.worker == nil {
		return CorrectionResult{}, ErrCorrectionUnavailable
	}
	return s.worker.CorrectRecent(ctx, limit)
}

// CorrectParagraph runs an explicit correction for one saved paragraph.
func (s *Settings) CorrectParagraph(ctx context.Context, id string) (CorrectionResult, error) {
	if s.worker == nil {
		return CorrectionResult{}, ErrCorrectionUnavailable
	}
	return s.worker.CorrectParagraph(ctx, id)
}

func (s *Settings) RetranscribeParagraph(ctx context.Context, id string) (ASRResult, error) {
	if s.worker == nil {
		return ASRResult{}, ErrASRUnavailable
	}
	return s.worker.RetranscribeParagraph(ctx, id)
}

func (s *Settings) SummarizeParagraph(ctx context.Context, id string) (SummaryResult, error) {
	if s.worker == nil {
		return SummaryResult{}, ErrSummaryUnavailable
	}
	return s.worker.SummarizeParagraph(ctx, id)
}

func (s *Settings) SplitRecent(ctx context.Context, limit int) (RebuildStats, error) {
	if s.worker == nil {
		return RebuildStats{}, ErrSplitUnavailable
	}
	return s.worker.SplitRecent(ctx, limit)
}

// apply pushes the current state to all runtime controllers. They may be nil in
// tests or when the corresponding feature is disabled.
func (s *Settings) apply() {
	s.mu.RLock()
	model := s.model
	correct := s.correct
	provider := s.asr
	controller := s.asrController
	s.mu.RUnlock()
	if s.client != nil {
		s.client.SetModel(model)
	}
	if s.worker != nil {
		s.worker.SetModel(model)
		s.worker.SetCorrectionEnabled(correct)
	}
	if controller != nil {
		if err := controller.SetASRProvider(provider); err != nil {
			s.logf("应用语音识别设置失败: %v", err)
		}
	}
}

func (s *Settings) write(model string, correct bool, provider string) error {
	if s.path == "" {
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	data, err := json.MarshalIndent(settingsFile{Version: settingsVersion, Model: model, Correct: correct, ASR: provider}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create settings temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write settings: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync settings: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close settings: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace settings: %w", err)
	}
	return nil
}

func hasControlChars(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}
