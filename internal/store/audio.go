package store

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	afconvertPath = "/usr/bin/afconvert"
	audioExtWAV   = ".wav"
	audioExtM4A   = ".m4a"
	encodeTimeout = 5 * time.Second
)

var (
	encodeAudio  = encodeAAC
	fallbackOnce sync.Once
)

// audioExt reports the container used for new recordings. DITING_AUDIO_FORMAT
// can force wav or m4a; otherwise AAC is used when afconvert is available.
func audioExt() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DITING_AUDIO_FORMAT"))) {
	case "wav":
		return audioExtWAV
	case "m4a":
		return audioExtM4A
	}
	if info, err := os.Stat(afconvertPath); err == nil && !info.IsDir() {
		return audioExtM4A
	}
	return audioExtWAV
}

func encodeAAC(wavPath, m4aPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), encodeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, afconvertPath,
		"-f", "m4af", "-d", "aac", "-b", "32000", "-c", "1", wavPath, m4aPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("afconvert: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func warnAudioFallback(err error) {
	fallbackOnce.Do(func() {
		log.Printf("音频压缩不可用，录音保留为 WAV: %v", err)
	})
}

func isAudioFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case audioExtWAV, audioExtM4A:
		return true
	}
	return false
}

func AudioMIME(path string) string {
	if strings.EqualFold(filepath.Ext(path), audioExtM4A) {
		return "audio/mp4"
	}
	return "audio/wav"
}
