package main

import (
	"context"
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

	"diting/internal/engine"
	"diting/internal/server"
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
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.models, "models", "models", "model directory")
	flag.StringVar(&cfg.data, "data", "data", "recording and transcript directory")
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:8080", "loopback HTTP address")
	flag.BoolVar(&cfg.autostart, "autostart", true, "start microphone on launch")
	flag.StringVar(&cfg.verifyWAV, "verify-wav", "", "verify recognition with a WAV file and exit")
	flag.BoolVar(&cfg.retranscribe, "retranscribe", false, "re-run all saved records with the current models and exit")
	flag.StringVar(&cfg.speakerModel, "speaker-model", "", "speaker embedding model (default: <models>/"+engine.SpeakerModelName+")")
	flag.Float64Var(&cfg.speakerThreshold, "speaker-threshold", 0.45, "cosine threshold for speaker matching")
	flag.Parse()

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
	if err := validateListenAddress(cfg.listen); err != nil {
		return err
	}

	segmentStore, err := store.Open(cfg.data)
	if err != nil {
		return err
	}
	defer segmentStore.Close()

	recognizer, err := engine.New(modelPaths(cfg), segmentStore, engineOptions(cfg))
	if err != nil {
		return err
	}
	defer recognizer.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	console := server.New(ctx, recognizer, segmentStore, web.Files)
	defer console.Close()

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

func modelPaths(cfg config) engine.ModelPaths {
	paths := engine.DefaultModelPaths(cfg.models)
	paths.Speaker = cfg.speakerModel
	return paths
}

func engineOptions(cfg config) engine.Options {
	return engine.Options{
		SpeakerRegistry:  filepath.Join(cfg.data, "speakers.json"),
		SpeakerThreshold: float32(cfg.speakerThreshold),
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

	recognizer, err := engine.New(modelPaths(cfg), segmentStore, engineOptions(cfg))
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

	recognizer, err := engine.New(modelPaths(cfg), segmentStore, engineOptions(cfg))
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
	return nil
}
