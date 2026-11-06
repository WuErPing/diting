package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

const sampleRate = 16000

func writeWAVFile(path string, samples []float32) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create WAV: %w", err)
	}

	dataSize := uint32(len(samples) * 2)
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], sampleRate*2)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	buffer := make([]byte, len(samples)*2)
	for i, sample := range samples {
		sample = max(-1, min(1, sample))
		value := int16(math.Round(float64(sample * 32767)))
		binary.LittleEndian.PutUint16(buffer[i*2:], uint16(value))
	}

	_, writeErr := file.Write(header)
	if writeErr == nil {
		_, writeErr = file.Write(buffer)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write WAV: %w", errors.Join(writeErr, closeErr))
	}
	return nil
}
