package engine

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/v2/sherpa_onnx"

	"diting/internal/store"
)

var (
	readWave     = sherpa.ReadWave
	convertToWAV = decodeToWAV
	decodeWarn   sync.Once
)

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
		wavePath := path
		var cleanup func()
		if !strings.EqualFold(filepath.Ext(path), ".wav") {
			wavePath, cleanup, err = convertToWAV(path)
			if err != nil {
				decodeWarn.Do(func() {
					log.Printf("部分录音无法解码为 WAV，已跳过: %v", err)
				})
				stats.MissingAudio++
				continue
			}
		}
		wave := readWave(wavePath)
		if cleanup != nil {
			cleanup()
		}
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

func decodeToWAV(path string) (string, func(), error) {
	file, err := os.CreateTemp("", "diting-decode-*.wav")
	if err != nil {
		return "", nil, err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	// afconvert wants to create the output itself.
	if err := os.Remove(name); err != nil {
		return "", nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "/usr/bin/afconvert",
		"-f", "WAVE", "-d", "LEI16@16000", "-c", "1", path, name).CombinedOutput()
	if err != nil {
		_ = os.Remove(name)
		return "", nil, fmt.Errorf("decode %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(string(output)))
	}
	// afconvert emits WAVE_FORMAT_EXTENSIBLE; sherpa only accepts canonical PCM.
	if err := canonicalizeWAV(name); err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	return name, func() { _ = os.Remove(name) }, nil
}

// canonicalizeWAV rewrites a 40-byte WAVE_FORMAT_EXTENSIBLE header as a plain
// 16-byte PCM header, keeping only the fmt and data chunks.
func canonicalizeWAV(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var riff [12]byte
	if _, err := io.ReadFull(file, riff[:]); err != nil {
		return fmt.Errorf("read wav header: %w", err)
	}
	if string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return errors.New("unsupported wav container")
	}

	var (
		formatTag  uint16
		channels   uint16
		sampleRate uint32
		byteRate   uint32
		blockAlign uint16
		bits       uint16
		fmtSize    uint32
		dataOffset int64
		dataSize   int64
	)
	foundFmt, foundData := false, false
	offset := int64(len(riff))
	for !foundFmt || !foundData {
		var chunk [8]byte
		if _, err := file.ReadAt(chunk[:], offset); err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("wav fmt or data chunk missing")
			}
			return err
		}
		id := string(chunk[0:4])
		size := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		payload := offset + 8
		switch id {
		case "fmt ":
			if size < 16 {
				return errors.New("invalid wav fmt chunk")
			}
			info := make([]byte, size)
			if _, err := file.ReadAt(info, payload); err != nil {
				return err
			}
			formatTag = binary.LittleEndian.Uint16(info[0:2])
			channels = binary.LittleEndian.Uint16(info[2:4])
			sampleRate = binary.LittleEndian.Uint32(info[4:8])
			byteRate = binary.LittleEndian.Uint32(info[8:12])
			blockAlign = binary.LittleEndian.Uint16(info[12:14])
			bits = binary.LittleEndian.Uint16(info[14:16])
			if formatTag == 0xFFFE {
				if len(info) < 26 || binary.LittleEndian.Uint16(info[24:26]) != 1 {
					return errors.New("unsupported wav subformat")
				}
				formatTag = 1
			}
			fmtSize = uint32(size)
			foundFmt = true
		case "data":
			dataOffset, dataSize = payload, size
			foundData = true
		}
		offset = payload + size + size%2
	}

	if formatTag != 1 {
		return fmt.Errorf("unsupported wav format %d", formatTag)
	}
	if fmtSize == 16 {
		return nil
	}

	out, err := os.CreateTemp(filepath.Dir(path), ".diting-wav-*.wav")
	if err != nil {
		return err
	}
	tempName := out.Name()
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(tempName)
		return err
	}

	var header [12]byte
	copy(header[0:4], "RIFF")
	riffSize := uint32(4 + 24 + 8 + dataSize + dataSize%2)
	binary.LittleEndian.PutUint32(header[4:8], riffSize)
	copy(header[8:12], "WAVE")

	var fmtChunk [24]byte
	copy(fmtChunk[0:4], "fmt ")
	binary.LittleEndian.PutUint32(fmtChunk[4:8], 16)
	binary.LittleEndian.PutUint16(fmtChunk[8:10], formatTag)
	binary.LittleEndian.PutUint16(fmtChunk[10:12], channels)
	binary.LittleEndian.PutUint32(fmtChunk[12:16], sampleRate)
	binary.LittleEndian.PutUint32(fmtChunk[16:20], byteRate)
	binary.LittleEndian.PutUint16(fmtChunk[20:22], blockAlign)
	binary.LittleEndian.PutUint16(fmtChunk[22:24], bits)

	var dataHeader [8]byte
	copy(dataHeader[0:4], "data")
	binary.LittleEndian.PutUint32(dataHeader[4:8], uint32(dataSize))

	if _, err := out.Write(header[:]); err != nil {
		return fail(err)
	}
	if _, err := out.Write(fmtChunk[:]); err != nil {
		return fail(err)
	}
	if _, err := out.Write(dataHeader[:]); err != nil {
		return fail(err)
	}
	if _, err := file.Seek(dataOffset, io.SeekStart); err != nil {
		return fail(err)
	}
	if _, err := io.CopyN(out, file, dataSize); err != nil {
		return fail(err)
	}
	if dataSize%2 == 1 {
		if _, err := out.Write([]byte{0}); err != nil {
			return fail(err)
		}
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	return nil
}
