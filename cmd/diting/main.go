package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"
	"github.com/wuerping/tokusage"

	"diting/internal/engine"
	"diting/internal/lexicon"
	"diting/internal/observability"
	"diting/internal/paragraph"
	"diting/internal/server"
	"diting/internal/speaker"
	"diting/internal/store"
	"diting/web"
)

type config struct {
	models           string
	data             string
	listen           string
	autostart        bool
	verifyWAV        string
	retranscribe     bool
	speakerModel     string
	speakerThreshold float64
	llmURL           string
	llmAPIKey        string
	llmModel         string
	llmCorrect       bool
	asrProvider      string
	merge            bool
	reparagraph      bool
	recent           int
	dryRun           bool
	restoreDay       string
	evalSpeakers     bool
	exportTestset    bool
	testsetPath      string
}

func main() {
	observability.Init(os.Getenv("DITING_LOG_LEVEL"))
	cfg := config{}
	flag.StringVar(&cfg.models, "models", "models", "model directory")
	flag.StringVar(&cfg.data, "data", "data", "recording and transcript directory")
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "loopback HTTP address")
	flag.BoolVar(&cfg.autostart, "autostart", true, "start microphone on launch")
	flag.StringVar(&cfg.verifyWAV, "verify-wav", "", "verify recognition with a WAV file and exit")
	flag.BoolVar(&cfg.retranscribe, "retranscribe", false, "re-run all saved records with the current models and exit")
	flag.StringVar(&cfg.speakerModel, "speaker-model", "", "speaker embedding model (default: <models>/"+engine.SpeakerModelName+")")
	flag.Float64Var(&cfg.speakerThreshold, "speaker-threshold", 0.45, "cosine threshold for speaker matching")
	flag.StringVar(&cfg.llmURL, "llm-url", paragraph.DefaultBaseURL, "OpenAI-compatible model API base URL (remote APIs require HTTPS)")
	flag.StringVar(&cfg.llmAPIKey, "llm-api-key", "", "model API key (default: DITING_LLM_API_KEY, MIMO_API_KEY, or OpenCode Xiaomi credentials)")
	flag.StringVar(&cfg.llmModel, "llm-model", paragraph.DefaultModel, "model used to identify meeting topics")
	flag.BoolVar(&cfg.llmCorrect, "llm-correct", true, "use the model to semantically correct paragraph text")
	flag.StringVar(&cfg.asrProvider, "asr", engine.ASRProviderLocal, "final transcription provider: local or mimo")
	flag.BoolVar(&cfg.merge, "merge", true, "merge consecutive same-speaker segments into paragraphs")
	flag.BoolVar(&cfg.reparagraph, "reparagraph", false, "rebuild paragraphs from existing transcripts and exit")
	flag.IntVar(&cfg.recent, "recent", 0, "with -reparagraph -dry-run, preview runs covering the most recent N segments")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "with -reparagraph -recent N, print before/after without writing")
	flag.StringVar(&cfg.restoreDay, "restore-day", "", "restore the latest snapshot for a day (YYYY-MM-DD) and exit")
	flag.BoolVar(&cfg.evalSpeakers, "eval-speakers", false, "evaluate speaker model against test set and exit")
	flag.BoolVar(&cfg.exportTestset, "export-testset", false, "export annotated segments as speaker test set and exit")
	flag.StringVar(&cfg.testsetPath, "testset", "", "test set JSONL path (default: <data>/testset.jsonl)")
	flag.Parse()
	if cfg.llmAPIKey == "" {
		cfg.llmAPIKey = defaultLLMAPIKey()
	}

	if cfg.speakerModel == "" {
		cfg.speakerModel = filepath.Join(cfg.models, engine.SpeakerModelName)
	}
	if err := run(cfg); err != nil {
		log.Printf("启动失败: %v", err)
		os.Exit(1)
	}
}

func run(cfg config) error {
	if cfg.verifyWAV != "" {
		return verify(cfg)
	}
	if cfg.retranscribe {
		return retranscribe(cfg)
	}
	if cfg.reparagraph {
		return reparagraph(cfg)
	}
	if cfg.restoreDay != "" {
		return restoreDay(cfg)
	}
	if cfg.evalSpeakers {
		return evalSpeakers(cfg)
	}
	if cfg.exportTestset {
		return exportTestset(cfg)
	}
	if err := validateListenAddress(cfg.listen); err != nil {
		return err
	}
	if cfg.merge {
		if err := paragraph.ValidateBaseURL(cfg.llmURL); err != nil {
			return err
		}
	}

	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	var onUsage func(model string, usage json.RawMessage, durationMs int64)
	recorder, err := tokusage.Open(tokusage.Options{
		LedgerPath: filepath.Join(cfg.data, "tokusage.jsonl"),
		ExportPath: filepath.Join(cfg.data, "tui-data-cache.json"),
		Logf:       log.Printf,
	})
	if err != nil {
		log.Printf("token 用量统计不可用（不影响转写）: %v", err)
	} else {
		defer recorder.Close()
		onUsage = func(model string, usage json.RawMessage, durationMs int64) {
			u, ok := tokusage.ParseOpenAIUsage(usage)
			if !ok {
				return
			}
			u.DurationMs = durationMs
			if err := recorder.Record(tokusage.Entry{Client: "diting", Provider: "mimo", Model: model, Usage: u}); err != nil {
				log.Printf("记录 token 用量失败: %v", err)
			}
		}
	}

	lex, err := lexicon.Open(filepath.Join(cfg.data, "lexicon.json"))
	if err != nil {
		return err
	}

	var paragraphStore *paragraph.Store
	var worker *paragraph.Worker
	var recognizer *engine.Engine
	var client *paragraph.Client
	var settings server.SettingsDirectory
	if cfg.merge {
		paragraphStore, err = paragraph.Open(cfg.data)
		if err != nil {
			return err
		}
		client = paragraph.NewClient(paragraph.ClientConfig{BaseURL: cfg.llmURL, APIKey: cfg.llmAPIKey, Model: cfg.llmModel, OnUsage: onUsage})
		worker = paragraph.NewWorker(paragraph.WorkerOptions{
			Store:      paragraphStore,
			Segments:   segmentStore,
			Grouper:    client,
			Corrector:  client,
			Summarizer: client,
			Retranscribe: func(ctx context.Context, selected []store.Segment) ([]store.Segment, int, error) {
				return engine.RetranscribeSegments(ctx, segmentStore, recognizer, selected)
			},
			Model: cfg.llmModel,
		})
		defer worker.Close()
	}

	options := engineOptions(cfg, lex, onUsage)
	if worker != nil {
		options.OnSegment = worker.Notify
	}
	recognizer, err = engine.New(modelPaths(cfg), segmentStore, options)
	if err != nil {
		return err
	}
	defer recognizer.Close()
	if cfg.merge {
		settings, err = paragraph.OpenSettings(filepath.Join(cfg.data, "settings.json"), client, worker,
			paragraph.Snapshot{Model: cfg.llmModel, Correct: cfg.llmCorrect, ASR: cfg.asrProvider}, log.Printf, recognizer)
		if err != nil {
			return err
		}
	}
	asrProvider := cfg.asrProvider
	if settings != nil {
		asrProvider = settings.Snapshot().ASR
	}
	log.Printf("ASR 模型: %s", asrModelName(asrProvider))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var reload func()
	if worker != nil {
		reload = worker.Reload
	}
	console := server.New(ctx, recognizer, segmentStore, paragraphStore, cfg.merge, lex, settings, reload, web.Files)
	defer console.Close()

	if worker != nil {
		worker.SetOnParagraph(console.PublishParagraph)
		worker.SetSpeakerResolver(recognizer.CanonicalSpeaker)
		worker.Start(ctx)
		go func() {
			if err := client.Check(ctx); err != nil {
				log.Printf("模型服务暂不可用，文稿将先保留整段且暂不生成主题: %v", err)
				return
			}
			log.Printf("会议议题模型: %s", settings.Snapshot().Model)
		}()
	}

	listener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.listen, err)
	}
	httpServer := &http.Server{
		Handler:           console.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- httpServer.Serve(listener)
	}()

	if cfg.autostart {
		if err := recognizer.Start(); err != nil {
			log.Printf("麦克风尚未启动: %v", err)
		}
	}
	log.Printf("谛听控制台: http://%s", cfg.listen)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveErrors:
	}

	recognizer.Stop()
	if worker != nil {
		if err := worker.Close(); err != nil {
			log.Printf("关闭段落合并器: %v", err)
		}
	}
	console.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

func defaultLLMAPIKey() string {
	if key := os.Getenv("DITING_LLM_API_KEY"); key != "" {
		return key
	}
	if key := os.Getenv("MIMO_API_KEY"); key != "" {
		return key
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if key := readAPIKeyFile(filepath.Join(home, "code", "keys", "xiaomi")); key != "" {
		return key
	}
	if key := readOpenCodeConfigAPIKey(filepath.Join(home, ".config", "opencode", "opencode.json")); key != "" {
		return key
	}
	return readOpenCodeAPIKey(filepath.Join(home, ".local", "share", "opencode", "auth.json"))
}

func readAPIKeyFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readOpenCodeConfigAPIKey(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var config struct {
		Provider struct {
			MiMo struct {
				APIKey string `json:"apiKey"`
			} `json:"mimo"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return ""
	}
	return strings.TrimSpace(config.Provider.MiMo.APIKey)
}

func readOpenCodeAPIKey(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var auth struct {
		Xiaomi struct {
			Key string `json:"key"`
		} `json:"xiaomi"`
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return ""
	}
	return auth.Xiaomi.Key
}

func modelPaths(cfg config) engine.ModelPaths {
	paths := engine.DefaultModelPaths(cfg.models)
	paths.Speaker = cfg.speakerModel
	return paths
}

func asrModelName(provider string) string {
	if provider == engine.ASRProviderMiMo {
		return engine.DefaultMiMoASRModel
	}
	return provider
}

func engineOptions(cfg config, lex *lexicon.Lexicon, onUsage func(model string, usage json.RawMessage, durationMs int64)) engine.Options {
	return engine.Options{
		SpeakerRegistry:  filepath.Join(cfg.data, "speakers.json"),
		SpeakerThreshold: float32(cfg.speakerThreshold),
		Lexicon:          lex,
		ASRProvider:      cfg.asrProvider,
		CloudASR: engine.NewMiMoASR(engine.MiMoASRConfig{
			BaseURL: cfg.llmURL,
			APIKey:  cfg.llmAPIKey,
			OnUsage: onUsage,
		}),
	}
}

func validateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	host = strings.Trim(strings.ToLower(host), "[]")
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("listen address must be loopback, got %q", host)
	}
	return nil
}

func verify(cfg config) error {
	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	lex, err := lexicon.Open(filepath.Join(cfg.data, "lexicon.json"))
	if err != nil {
		return err
	}
	recognizer, err := engine.New(modelPaths(cfg), segmentStore, engineOptions(cfg, lex, nil))
	if err != nil {
		return err
	}
	defer recognizer.Close()

	wave := sherpa.ReadWave(cfg.verifyWAV)
	if wave == nil {
		return fmt.Errorf("read WAV %s", cfg.verifyWAV)
	}
	if wave.SampleRate != engine.SampleRate {
		return fmt.Errorf("WAV sample rate must be %d", engine.SampleRate)
	}
	refined := recognizer.RefineWAV(wave.Samples)
	if err := recognizer.ProcessWAV(wave.Samples); err != nil {
		return err
	}
	segments := segmentStore.List(1, time.Time{})
	if len(segments) == 0 {
		return fmt.Errorf("no speech segment detected")
	}
	interimCount, finalCount := 0, 0
	for {
		select {
		case event := <-recognizer.Events():
			switch event.Type {
			case "interim":
				interimCount++
			case "segment":
				finalCount++
			}
		default:
			fmt.Printf("整段精校: %s\n识别文本: %s\n说话人: %s\n录音文件: %s\n实时事件: %d 条 interim / %d 条 final\n", refined, segments[0].Text, segments[0].Speaker, segments[0].AudioPath, interimCount, finalCount)
			return nil
		}
	}
}

func retranscribe(cfg config) error {
	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	lex, err := lexicon.Open(filepath.Join(cfg.data, "lexicon.json"))
	if err != nil {
		return err
	}
	recognizer, err := engine.New(modelPaths(cfg), segmentStore, engineOptions(cfg, lex, nil))
	if err != nil {
		return err
	}
	defer recognizer.Close()

	stats, err := engine.RetranscribeAll(segmentStore, recognizer)
	if err != nil {
		return err
	}
	fmt.Printf("重新转写完成: 总数=%d 更新=%d 缺少录音=%d 无文本=%d\n", stats.Total, stats.Updated, stats.MissingAudio, stats.EmptyText)
	fmt.Printf("原记录已备份到 %s\n", filepath.Join(cfg.data, "transcripts.jsonl.bak"))

	paragraphStore, err := paragraph.Open(cfg.data)
	if err != nil {
		return err
	}
	if err := paragraphStore.Reset(); err != nil {
		return err
	}
	if !cfg.merge {
		fmt.Println("段落缓存已清空（-merge=false）")
		return nil
	}
	if err := paragraph.ValidateBaseURL(cfg.llmURL); err != nil {
		return err
	}
	segments, err := segmentStore.All()
	if err != nil {
		return err
	}
	client := paragraph.NewClient(paragraph.ClientConfig{BaseURL: cfg.llmURL, APIKey: cfg.llmAPIKey, Model: cfg.llmModel})
	rebuildStats, err := paragraph.Rebuild(context.Background(), paragraphStore, segments, client, paragraphCorrector(cfg, client),
		cfg.llmModel, paragraph.RunOptions{}, nil, recognizer.CanonicalSpeaker, progressPrinter("段落重建"))
	if err != nil {
		return err
	}
	printRebuildStats(rebuildStats)
	return nil
}

func reparagraph(cfg config) error {
	if !cfg.merge {
		return fmt.Errorf("-reparagraph 需要 -merge=true")
	}
	if err := paragraph.ValidateBaseURL(cfg.llmURL); err != nil {
		return err
	}
	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	paragraphStore, err := paragraph.Open(cfg.data)
	if err != nil {
		return err
	}
	segments, err := segmentStore.All()
	if err != nil {
		return err
	}

	var resolve func(string) string
	if mergeMap := loadSpeakerMerges(filepath.Join(cfg.data, "speakers.json")); len(mergeMap) > 0 {
		resolve = func(label string) string {
			seen := make(map[string]struct{}, len(mergeMap))
			current := label
			for {
				if _, ok := seen[current]; ok {
					return label
				}
				seen[current] = struct{}{}
				next, ok := mergeMap[current]
				if !ok || next == "" {
					return current
				}
				current = next
			}
		}
	}

	client := paragraph.NewClient(paragraph.ClientConfig{BaseURL: cfg.llmURL, APIKey: cfg.llmAPIKey, Model: cfg.llmModel})

	if cfg.recent > 0 && !cfg.dryRun {
		return fmt.Errorf("-recent 仅用于 -dry-run 预览；要写入请去掉 -recent（全量重建段落）")
	}

	if cfg.dryRun {
		if cfg.recent <= 0 {
			return fmt.Errorf("-dry-run 需要 -recent N（N>0）")
		}
		comparisons, err := paragraph.CompareRecent(context.Background(), paragraphStore, segments, client,
			paragraphCorrector(cfg, client), cfg.llmModel, paragraph.RunOptions{}, nil, resolve, cfg.recent)
		if err != nil {
			return err
		}
		printComparisons(comparisons, cfg.llmModel)
		return nil
	}

	stats, err := paragraph.Rebuild(context.Background(), paragraphStore, segments, client, paragraphCorrector(cfg, client),
		cfg.llmModel, paragraph.RunOptions{}, nil, resolve, progressPrinter("段落重建"))
	if err != nil {
		return err
	}
	printRebuildStats(stats)
	return nil
}

func printComparisons(comparisons []paragraph.Comparison, model string) {
	if len(comparisons) == 0 {
		fmt.Println("没有可比较的段落")
		return
	}
	fmt.Printf("模型 %s 试运行（不写入）：共 %d 段\n\n", model, len(comparisons))
	changed := 0
	for i, c := range comparisons {
		distance := paragraph.EditDistance(c.Before, c.After)
		if distance > 0 {
			changed++
		}
		fmt.Printf("[%d] 说话人 %s  %s  差异 %d 字\n", i+1, c.Speaker, c.StartedAt.Format("2006-01-02 15:04"), distance)
		fmt.Printf("  ASR原文: %s\n", c.Raw)
		fmt.Printf("  现行   : %s\n", c.Before)
		fmt.Printf("  新模型 : %s\n\n", c.After)
	}
	fmt.Printf("合计 %d/%d 段有变更（新模型 vs 现行）。\n", changed, len(comparisons))
	fmt.Println("提示：现行与新模型的差异仅来自 LLM 补正；若 ASR原文 本身有误，切换模型无法修正。")
}

func paragraphCorrector(cfg config, client *paragraph.Client) paragraph.Corrector {
	if !cfg.llmCorrect {
		return nil
	}
	return client
}

func progressPrinter(label string) func(done, total int) {
	return func(done, total int) {
		if done%20 == 0 || done == total {
			log.Printf("%s %d/%d", label, done, total)
		}
	}
}

func printRebuildStats(stats paragraph.RebuildStats) {
	fmt.Printf("段落重建完成: run=%d 模型=%d 单段=%d 回退=%d 段落=%d\n",
		stats.Runs, stats.LLM, stats.Single, stats.Fallback, stats.Paragraphs)
}

func loadSpeakerMerges(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var file struct {
		Speakers []struct {
			Name       string `json:"name"`
			MergedInto string `json:"mergedInto"`
		} `json:"speakers"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil
	}
	merges := make(map[string]string)
	for _, s := range file.Speakers {
		if s.MergedInto != "" {
			merges[s.Name] = s.MergedInto
		}
	}
	return merges
}

func restoreDay(cfg config) error {
	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()
	result, err := segmentStore.RestoreDay(cfg.restoreDay)
	if err != nil {
		return err
	}
	fmt.Printf("恢复完成: 日期=%s 恢复=%d 跳过=%d 快照=%s\n", result.Date, result.Restored, result.Skipped, result.Snapshot)
	if result.Restored > 0 {
		fmt.Println("提示：录音文件仍需从废纸篓手动拖回 data/audio/ 对应目录后才能回放。")
	}
	return nil
}

func evalSpeakers(cfg config) error {
	testsetPath := cfg.testsetPath
	if testsetPath == "" {
		testsetPath = filepath.Join(cfg.data, "testset.jsonl")
	}
	entries, err := speaker.LoadTestSet(testsetPath)
	if err != nil {
		return err
	}

	paths := modelPaths(cfg)
	extractor, err := engine.NewSpeakerExtractor(paths)
	if err != nil {
		return err
	}
	defer sherpa.DeleteSpeakerEmbeddingExtractor(extractor)

	fmt.Printf("模型: %s (维度 %d)\n", filepath.Base(paths.Speaker), extractor.Dim())
	fmt.Printf("测试集: %s (%d 条)\n\n", testsetPath, len(entries))

	embeddings := make([][]float32, 0, len(entries))
	labels := make([]string, 0, len(entries))
	skipped := 0

	for i, entry := range entries {
		audioPath := entry.Audio
		if !filepath.IsAbs(audioPath) {
			audioPath = filepath.Join(cfg.data, filepath.FromSlash(audioPath))
		}
		samples, err := loadAudioSamples(audioPath, entry.Offset, entry.Limit)
		if err != nil {
			skipped++
			if skipped <= 3 {
				log.Printf("跳过 %s: %v", entry.Audio, err)
			}
			continue
		}
		embedding := extractEmbedding(extractor, samples)
		if embedding == nil {
			skipped++
			continue
		}
		embeddings = append(embeddings, embedding)
		labels = append(labels, entry.Speaker)
		if (i+1)%50 == 0 {
			log.Printf("提取进度: %d/%d", i+1, len(entries))
		}
	}

	if skipped > 0 {
		fmt.Printf("跳过 %d 条（音频缺失或无法提取）\n\n", skipped)
	}
	if len(embeddings) < 2 {
		return fmt.Errorf("有效数据不足（%d 条），无法评估", len(embeddings))
	}

	threshold := float32(cfg.speakerThreshold)
	result := speaker.Evaluate(embeddings, labels, threshold)
	fmt.Println(speaker.FormatResult(result))
	suggested := speaker.SuggestThreshold(result)
	fmt.Printf("\n建议阈值: %.3f\n", suggested)
	return nil
}

func speakerRegistryDimension(path string, fallback int) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	var metadata struct {
		Dim int `json:"dim"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.Dim <= 0 {
		return fallback
	}
	return metadata.Dim
}

func exportTestset(cfg config) error {
	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	segments, err := segmentStore.All()
	if err != nil {
		return err
	}

	registryPath := filepath.Join(cfg.data, "speakers.json")
	dimension := speakerRegistryDimension(registryPath, 192)
	registry, err := speaker.Open(registryPath, dimension, float32(cfg.speakerThreshold))
	if err != nil {
		registry = nil
	}

	outPath := cfg.testsetPath
	if outPath == "" {
		outPath = filepath.Join(cfg.data, "testset.jsonl")
	}
	file, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create test set file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	encoder := json.NewEncoder(writer)
	exported := 0
	for _, segment := range segments {
		if segment.AudioPath == "" || segment.Speaker == "" {
			continue
		}
		label := segment.Speaker
		if registry != nil {
			label = registry.DisplayName(segment.Speaker)
		}
		entry := speaker.TestEntry{
			Audio:   segment.AudioPath,
			Speaker: label,
		}
		if err := encoder.Encode(entry); err != nil {
			return err
		}
		exported++
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	fmt.Printf("已导出 %d 条到 %s\n", exported, outPath)
	fmt.Println("请手动检查并修正说话人标注，确保 ground truth 准确。")
	return nil
}

func loadAudioSamples(path string, offset, limit float64) ([]float32, error) {
	samples, err := engine.LoadAudioSamples(path)
	if err != nil {
		return nil, err
	}
	if offset > 0 || limit > 0 {
		start := int(offset * float64(engine.SampleRate))
		if start > len(samples) {
			return nil, fmt.Errorf("offset 超出音频长度")
		}
		end := len(samples)
		if limit > 0 {
			end = min(start+int(limit*float64(engine.SampleRate)), len(samples))
		}
		samples = samples[start:end]
	}
	if len(samples) < engine.SampleRate/2 {
		return nil, fmt.Errorf("音频太短")
	}
	return samples, nil
}

func extractEmbedding(extractor *sherpa.SpeakerEmbeddingExtractor, samples []float32) []float32 {
	embedding, _ := engine.ExtractSpeakerEmbedding(extractor, samples)
	return embedding
}
