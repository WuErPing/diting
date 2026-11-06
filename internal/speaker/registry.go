package speaker

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	registryVersion   = 1
	minEnrollDuration = 2 * time.Second
)

type Profile struct {
	Name      string    `json:"name"`
	Centroid  []float32 `json:"centroid"`
	Count     int       `json:"count"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type registryFile struct {
	Version  int       `json:"version"`
	Dim      int       `json:"dim"`
	NextID   int       `json:"nextId"`
	Speakers []Profile `json:"speakers"`
}

type Registry struct {
	mu        sync.Mutex
	path      string
	dim       int
	threshold float32
	nextID    int
	profiles  []Profile
}

func Open(path string, dim int, threshold float32) (*Registry, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("speaker embedding dimension must be positive")
	}
	if threshold <= 0 || threshold > 1 {
		threshold = 0.6
	}
	registry := &Registry{path: path, dim: dim, threshold: threshold, nextID: 1}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read speaker registry %s: %w", path, err)
	}

	var file registryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse speaker registry %s (delete or restore it to rebuild): %w", path, err)
	}
	if file.Dim != 0 && file.Dim != dim {
		return nil, fmt.Errorf("speaker registry dimension %d does not match model %d; delete or restore %s", file.Dim, dim, path)
	}
	for _, profile := range file.Speakers {
		centroid := Normalize(profile.Centroid)
		if centroid == nil || len(centroid) != dim {
			return nil, fmt.Errorf("speaker registry %s has an invalid centroid for %q", path, profile.Name)
		}
		profile.Centroid = centroid
		if profile.Count <= 0 {
			profile.Count = 1
		}
		registry.profiles = append(registry.profiles, profile)
	}
	registry.nextID = file.NextID
	if registry.nextID <= 0 {
		registry.nextID = len(registry.profiles) + 1
	}
	return registry, nil
}

// Identify returns the matching speaker name, enrolling a new speaker when the
// segment is long enough to produce a reliable voiceprint.
func (r *Registry) Identify(embedding []float32, duration time.Duration) string {
	normalized := Normalize(embedding)
	if normalized == nil || len(normalized) != r.dim {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	best, _ := Assign(r.profiles, normalized, r.threshold)
	if best >= 0 {
		profile := &r.profiles[best]
		profile.Centroid = Normalize(blend(profile.Centroid, profile.Count, normalized))
		profile.Count++
		profile.UpdatedAt = time.Now().UTC()
		return profile.Name
	}

	if duration < minEnrollDuration {
		return ""
	}

	profile := Profile{
		Name:      fmt.Sprintf("说话人%d", r.nextID),
		Centroid:  normalized,
		Count:     1,
		UpdatedAt: time.Now().UTC(),
	}
	r.nextID++
	r.profiles = append(r.profiles, profile)
	if err := r.saveLocked(); err != nil {
		log.Printf("保存声纹注册表失败: %v", err)
	}
	return profile.Name
}

func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.profiles)
}

func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create speaker registry directory: %w", err)
	}
	data, err := json.MarshalIndent(registryFile{
		Version:  registryVersion,
		Dim:      r.dim,
		NextID:   r.nextID,
		Speakers: r.profiles,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode speaker registry: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(dir, ".speakers-*.json")
	if err != nil {
		return fmt.Errorf("create speaker registry temp file: %w", err)
	}
	tmp := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write speaker registry: %w", err)
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync speaker registry: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close speaker registry: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace speaker registry: %w", err)
	}
	return nil
}

// Assign returns the closest profile index and cosine score. A negative index
// means no profile reached the threshold.
func Assign(profiles []Profile, embedding []float32, threshold float32) (int, float32) {
	best, bestScore := -1, float32(0)
	for i, profile := range profiles {
		score := Cosine(embedding, profile.Centroid)
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if bestScore < threshold {
		return -1, bestScore
	}
	return best, bestScore
}

func Normalize(vector []float32) []float32 {
	if len(vector) == 0 {
		return nil
	}
	var sum float64
	for _, value := range vector {
		sum += float64(value) * float64(value)
	}
	if sum == 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
		return nil
	}
	norm := math.Sqrt(sum)
	out := make([]float32, len(vector))
	for i, value := range vector {
		out[i] = float32(float64(value) / norm)
	}
	return out
}

func Cosine(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	denominator := math.Sqrt(normA * normB)
	if denominator == 0 {
		return 0
	}
	return float32(dot / denominator)
}

func blend(centroid []float32, count int, embedding []float32) []float32 {
	sum := make([]float32, len(centroid))
	weight := float32(count)
	for i := range centroid {
		sum[i] = centroid[i]*weight + embedding[i]
	}
	return sum
}
