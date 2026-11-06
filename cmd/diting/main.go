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
	"strings"
	"syscall"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/engine"
	"diting/internal/server"
	"diting/internal/store"
	"diting/web"
)

func main() {
	models := flag.String("models", "models", "model directory")
	data := flag.String("data", "data", "recording and transcript directory")
	listen := flag.String("listen", "127.0.0.1:8080", "loopback HTTP address")
	autostart := flag.Bool("autostart", true, "start microphone on launch")
	verifyWAV := flag.String("verify-wav", "", "verify recognition with a WAV file and exit")
	flag.Parse()

	if err := run(*models, *data, *listen, *autostart, *verifyWAV); err != nil {
		log.Printf("启动失败: %v", err)
		os.Exit(1)
	}
}

func run(models, data, listen string, autostart bool, verifyWAV string) error {
	if verifyWAV != "" {
		return verify(models, data, verifyWAV)
	}
	if err := validateListenAddress(listen); err != nil {
		return err
	}

	segmentStore, err := store.Open(data)
	if err != nil {
		return err
	}
	recognizer, err := engine.New(engine.DefaultModelPaths(models), segmentStore)
	if err != nil {
		return err
	}
	defer recognizer.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	console := server.New(ctx, recognizer, segmentStore, web.Files)
	defer console.Close()

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
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

	if autostart {
		if err := recognizer.Start(); err != nil {
			log.Printf("麦克风尚未启动: %v", err)
		}
	}
	log.Printf("谛听控制台: http://%s", listen)

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

func verify(models, data, verifyWAV string) error {
	segmentStore, err := store.Open(data)
	if err != nil {
		return err
	}
	recognizer, err := engine.New(engine.DefaultModelPaths(models), segmentStore)
	if err != nil {
		return err
	}
	defer recognizer.Close()

	wave := sherpa.ReadWave(verifyWAV)
	if wave == nil {
		return fmt.Errorf("read WAV %s", verifyWAV)
	}
	if wave.SampleRate != engine.SampleRate {
		return fmt.Errorf("WAV sample rate must be %d", engine.SampleRate)
	}
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
			fmt.Printf("识别文本: %s\n录音文件: %s\n实时事件: %d 条 interim / %d 条 final\n", segments[0].Text, segments[0].AudioPath, interimCount, finalCount)
			return nil
		}
	}
}
