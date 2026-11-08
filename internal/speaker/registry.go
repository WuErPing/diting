package speaker

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	registryVersion     = 1
	minEnrollDuration   = 3 * time.Second
	minUpdateDuration   = 2 * time.Second
	maxBlendWeight      = 20
	enrollMargin        = float32(0.08)
	updateMargin        = float32(0.03)
	minEmbeddingQuality = float32(0.6)
	maxAliasRunes       = 32
)

var (
	ErrUnknownSpeaker = errors.New("说话人不存在")
	ErrInvalidAlias   = errors.New("人名无效")
	ErrMergedSpeaker  = errors.New("说话人已合并，请先取消合并")
	ErrInvalidMerge   = errors.New("合并无效")
)

type Profile struct {
	Name       string    `json:"name"`
	Alias      string    `json:"alias,omitempty"`
	MergedInto string    `json:"mergedInto,omitempty"`
	Centroid   []float32 `json:"centroid"`
	Count      int       `json:"count"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type View struct {
	Label      string    `json:"label"`
	Name       string    `json:"name"`
	MergedInto string    `json:"mergedInto,omitempty"`
	Count      int       `json:"count"`
	UpdatedAt  time.Time `json:"updatedAt"`
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

// Identify returns the matching speaker name. Short segments only match; they
// never update a centroid. A new speaker is enrolled only when the best score
// is clearly below the threshold, which avoids fragmenting one voice.
func (r *Registry) Identify(embedding []float32, duration time.Duration) string {
	return r.IdentifyWithQuality(embedding, duration, 1)
}

func (r *Registry) IdentifyWithQuality(embedding []float32, duration time.Duration, quality float32) string {
	normalized := Normalize(embedding)
	if normalized == nil || len(normalized) != r.dim {
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.profiles) > 0 {
		best, score := closest(r.profiles, normalized)
		secondScore := float32(-1)
		for i, candidate := range r.profiles {
			if i != best {
				secondScore = max(secondScore, Cosine(normalized, candidate.Centroid))
			}
		}
		profile := &r.profiles[best]
		if score >= r.threshold {
			if duration >= minUpdateDuration && quality >= minEmbeddingQuality &&
				score >= r.threshold+updateMargin && score-secondScore >= updateMargin {
				profile.Centroid = Normalize(blend(profile.Centroid, profile.Count, normalized))
				profile.Count++
				profile.UpdatedAt = time.Now().UTC()
			}
			return profile.Name
		}
		if score >= r.threshold-enrollMargin {
			return profile.Name
		}
	}

	if duration < minEnrollDuration || quality < minEmbeddingQuality {
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

// DisplayName resolves a stable label to its human alias, following merge chains.
func (r *Registry) DisplayName(label string) string {
	if label == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	canonical := r.canonicalLocked(label)
	for i := range r.profiles {
		if r.profiles[i].Name == canonical {
			if r.profiles[i].Alias != "" {
				return r.profiles[i].Alias
			}
			return canonical
		}
	}
	return canonical
}

// CanonicalLabel follows the mergedInto chain to find the root speaker label.
func (r *Registry) CanonicalLabel(label string) string {
	if label == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.canonicalLocked(label)
}

func (r *Registry) canonicalLocked(label string) string {
	seen := make(map[string]struct{}, len(r.profiles))
	current := label
	for {
		if _, ok := seen[current]; ok {
			return label
		}
		seen[current] = struct{}{}
		found := false
		for i := range r.profiles {
			if r.profiles[i].Name == current {
				if r.profiles[i].MergedInto == "" {
					return current
				}
				current = r.profiles[i].MergedInto
				found = true
				break
			}
		}
		if !found {
			return current
		}
	}
}

func (r *Registry) Views() []View {
	r.mu.Lock()
	defer r.mu.Unlock()
	views := make([]View, len(r.profiles))
	for i, profile := range r.profiles {
		name := profile.Alias
		if name == "" {
			name = profile.Name
		}
		if profile.MergedInto != "" {
			canonical := r.canonicalLocked(profile.Name)
			for j := range r.profiles {
				if r.profiles[j].Name == canonical {
					if r.profiles[j].Alias != "" {
						name = r.profiles[j].Alias
					} else {
						name = canonical
					}
					break
				}
			}
		}
		views[i] = View{
			Label:      profile.Name,
			Name:       name,
			MergedInto: profile.MergedInto,
			Count:      profile.Count,
			UpdatedAt:  profile.UpdatedAt,
		}
	}
	return views
}

// SetAlias assigns or clears a human name for a stable speaker label.
// If the alias matches another speaker's alias, this speaker is automatically
// merged into that one (same name = same person).
func (r *Registry) SetAlias(label, alias string) error {
	alias = strings.TrimSpace(alias)
	if err := validateAlias(alias); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	index := -1
	for i := range r.profiles {
		if r.profiles[i].Name == label {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSpeaker, label)
	}
	if r.profiles[index].MergedInto != "" {
		return fmt.Errorf("%w: %s 已合并到 %s", ErrMergedSpeaker, label, r.profiles[index].MergedInto)
	}

	// Same alias as another speaker → auto-merge into that speaker.
	if alias != "" {
		for i, profile := range r.profiles {
			if i == index {
				continue
			}
			if profile.Name == alias {
				return fmt.Errorf("%w: %q 与稳定标签冲突", ErrInvalidAlias, alias)
			}
			if profile.MergedInto != "" {
				continue
			}
			if profile.Alias == alias {
				target := r.canonicalLocked(profile.Name)
				previousMerge := r.profiles[index].MergedInto
				previousAlias := r.profiles[index].Alias
				r.profiles[index].MergedInto = target
				r.profiles[index].Alias = ""
				if err := r.saveLocked(); err != nil {
					r.profiles[index].MergedInto = previousMerge
					r.profiles[index].Alias = previousAlias
					return err
				}
				return nil
			}
		}
	}

	previous := r.profiles[index].Alias
	r.profiles[index].Alias = alias
	if err := r.saveLocked(); err != nil {
		r.profiles[index].Alias = previous
		return err
	}
	return nil
}

// SetMerge merges label into target, or unmerges if target is empty.
func (r *Registry) SetMerge(label, target string) error {
	target = strings.TrimSpace(target)
	r.mu.Lock()
	defer r.mu.Unlock()

	index := -1
	for i := range r.profiles {
		if r.profiles[i].Name == label {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%w: %s", ErrUnknownSpeaker, label)
	}

	if target == "" {
		if r.profiles[index].MergedInto == "" {
			return nil
		}
		previous := r.profiles[index].MergedInto
		r.profiles[index].MergedInto = ""
		if err := r.saveLocked(); err != nil {
			r.profiles[index].MergedInto = previous
			return err
		}
		return nil
	}

	if target == label {
		return fmt.Errorf("%w: 不能合并到自己", ErrInvalidMerge)
	}
	targetIndex := -1
	for i := range r.profiles {
		if r.profiles[i].Name == target {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return fmt.Errorf("%w: 目标 %s 不存在", ErrUnknownSpeaker, target)
	}
	// Check for cycles: follow target's chain; if we reach label, it's a cycle.
	current := target
	for steps := 0; steps < len(r.profiles); steps++ {
		if current == label {
			return fmt.Errorf("%w: 会形成循环合并", ErrInvalidMerge)
		}
		next := ""
		for i := range r.profiles {
			if r.profiles[i].Name == current {
				next = r.profiles[i].MergedInto
				break
			}
		}
		if next == "" {
			break
		}
		current = next
	}

	previousMerge := r.profiles[index].MergedInto
	previousAlias := r.profiles[index].Alias
	r.profiles[index].MergedInto = target
	r.profiles[index].Alias = ""
	if err := r.saveLocked(); err != nil {
		r.profiles[index].MergedInto = previousMerge
		r.profiles[index].Alias = previousAlias
		return err
	}
	return nil
}

func validateAlias(alias string) error {
	if utf8.RuneCountInString(alias) > maxAliasRunes {
		return fmt.Errorf("%w: 不能超过 %d 个字", ErrInvalidAlias, maxAliasRunes)
	}
	for _, character := range alias {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: 不能包含控制字符", ErrInvalidAlias)
		}
	}
	return nil
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
	best, bestScore := closest(profiles, embedding)
	if best < 0 || bestScore < threshold {
		return -1, bestScore
	}
	return best, bestScore
}

func closest(profiles []Profile, embedding []float32) (int, float32) {
	best, bestScore := -1, float32(-1)
	for i, profile := range profiles {
		score := Cosine(embedding, profile.Centroid)
		if score > bestScore {
			best, bestScore = i, score
		}
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
	if weight > maxBlendWeight {
		weight = maxBlendWeight
	}
	for i := range centroid {
		sum[i] = centroid[i]*weight + embedding[i]
	}
	return sum
}
