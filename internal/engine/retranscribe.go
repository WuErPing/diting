package engine

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/store"
)

var readWave = sherpa.ReadWave

type Processor interface {
	RefineWAV([]float32) string
	IdentifySamples([]float32, time.Duration) string
}

type History interface {
	Root() string
	All() ([]store.Segment, error)
	Rewrite([]store.Segment) error
}

type Stats struct {
	Total        int
	Updated      int
	MissingAudio int
	EmptyText    int
}

func RetranscribeAll(history History, processor Processor) (Stats, error) {
	segments, err := history.All()
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Total: len(segments)}
	root := history.Root()

	for i := range segments {
		segment := &segments[i]
		path, err := resolveAudioPath(root, segment.AudioPath)
		if err != nil {
			stats.MissingAudio++
			continue
		}
		wave := readWave(path)
		if wave == nil || wave.SampleRate != SampleRate {
			stats.MissingAudio++
			continue
		}

		text := strings.TrimSpace(processor.RefineWAV(wave.Samples))
		if text == "" {
			stats.EmptyText++
		} else {
			segment.Text = text
			stats.Updated++
		}
		segment.Speaker = processor.IdentifySamples(wave.Samples, time.Duration(segment.DurationMS)*time.Millisecond)

		if (i+1)%50 == 0 {
			log.Printf("重新转写进度: %d/%d", i+1, len(segments))
		}
	}

	if err := history.Rewrite(segments); err != nil {
		return stats, err
	}
	return stats, nil
}

func resolveAudioPath(root, relative string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	cleanRoot := filepath.Clean(root) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(path), cleanRoot) {
		return "", fmt.Errorf("audio path %q escapes data directory", relative)
	}
	return path, nil
}
