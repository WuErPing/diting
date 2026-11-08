package speaker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

type TestEntry struct {
	Audio   string  `json:"audio"`
	Speaker string  `json:"speaker"`
	Offset  float64 `json:"offset,omitempty"`
	Limit   float64 `json:"limit,omitempty"`
}

func LoadTestSet(path string) ([]TestEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open test set: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var entries []TestEntry
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry TestEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("parse test set entry: %w", err)
		}
		if entry.Audio == "" || entry.Speaker == "" {
			return nil, fmt.Errorf("test set entry missing audio or speaker field")
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read test set: %w", err)
	}
	if len(entries) < 2 {
		return nil, fmt.Errorf("test set needs at least 2 entries, got %d", len(entries))
	}
	return entries, nil
}

type EvalResult struct {
	Total      int
	Speakers   int
	Pairs      int
	Positives  int
	Negatives  int
	EER        float32
	EERThresh  float32
	Accuracy   float32
	Threshold  float32
	PosMean    float32
	PosMin     float32
	PosMax     float32
	NegMean    float32
	NegMin     float32
	NegMax     float32
	Separation float32
}

func Evaluate(embeddings [][]float32, labels []string, threshold float32) EvalResult {
	if len(embeddings) != len(labels) {
		return EvalResult{}
	}

	normalized := make([][]float32, len(embeddings))
	valid := make([]bool, len(embeddings))
	for i, emb := range embeddings {
		n := Normalize(emb)
		normalized[i] = n
		valid[i] = n != nil
	}

	speakerSet := make(map[string]struct{})
	for _, label := range labels {
		speakerSet[label] = struct{}{}
	}

	var positives, negatives []float32
	for i := 0; i < len(labels); i++ {
		if !valid[i] {
			continue
		}
		for j := i + 1; j < len(labels); j++ {
			if !valid[j] {
				continue
			}
			score := Cosine(normalized[i], normalized[j])
			if labels[i] == labels[j] {
				positives = append(positives, score)
			} else {
				negatives = append(negatives, score)
			}
		}
	}

	result := EvalResult{
		Total:     len(labels),
		Speakers:  len(speakerSet),
		Pairs:     len(positives) + len(negatives),
		Positives: len(positives),
		Negatives: len(negatives),
		Threshold: threshold,
	}
	if len(positives) == 0 || len(negatives) == 0 {
		return result
	}

	result.PosMean, result.PosMin, result.PosMax = scoreStats(positives)
	result.NegMean, result.NegMin, result.NegMax = scoreStats(negatives)
	result.Separation = result.PosMean - result.NegMean

	tp, fp, tn, fn := classify(positives, negatives, threshold)
	if total := tp + fp + tn + fn; total > 0 {
		result.Accuracy = float32(tp+tn) / float32(total)
	}

	result.EER, result.EERThresh = computeEER(positives, negatives)
	return result
}

func classify(positives, negatives []float32, threshold float32) (tp, fp, tn, fn int) {
	for _, score := range positives {
		if score >= threshold {
			tp++
		} else {
			fn++
		}
	}
	for _, score := range negatives {
		if score >= threshold {
			fp++
		} else {
			tn++
		}
	}
	return
}

func computeEER(positives, negatives []float32) (eer, threshold float32) {
	all := make([]float32, 0, len(positives)+len(negatives))
	all = append(all, positives...)
	all = append(all, negatives...)
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	posSorted := append([]float32(nil), positives...)
	sort.Slice(posSorted, func(i, j int) bool { return posSorted[i] < posSorted[j] })
	negSorted := append([]float32(nil), negatives...)
	sort.Slice(negSorted, func(i, j int) bool { return negSorted[i] < negSorted[j] })

	bestEER := float32(1)
	bestThresh := float32(0.5)
	for _, candidate := range all {
		far := fractionAbove(negSorted, candidate)
		frr := fractionBelow(posSorted, candidate)
		avg := (far + frr) / 2
		if avg < bestEER {
			bestEER = avg
			bestThresh = candidate
		}
	}
	return bestEER, bestThresh
}

func fractionAbove(sorted []float32, threshold float32) float32 {
	idx := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= threshold })
	return float32(len(sorted)-idx) / float32(len(sorted))
}

func fractionBelow(sorted []float32, threshold float32) float32 {
	idx := sort.Search(len(sorted), func(i int) bool { return sorted[i] >= threshold })
	return float32(idx) / float32(len(sorted))
}

func scoreStats(scores []float32) (mean, min, max float32) {
	if len(scores) == 0 {
		return
	}
	min, max = scores[0], scores[0]
	var sum float64
	for _, s := range scores {
		sum += float64(s)
		if s < min {
			min = s
		}
		if s > max {
			max = s
		}
	}
	mean = float32(sum / float64(len(scores)))
	return
}

func FormatResult(result EvalResult) string {
	if result.Pairs == 0 {
		return "无法评估：没有有效的配对数据"
	}
	return fmt.Sprintf(`测试集: %d 条录音, %d 个说话人, %d 对比较 (%d 正 / %d 负)
EER: %.2f%% (最佳阈值 %.3f)
当前阈值 %.3f 准确率: %.2f%%
正对分数: mean=%.4f min=%.4f max=%.4f
负对分数: mean=%.4f min=%.4f max=%.4f
分离度: %.4f`,
		result.Total, result.Speakers, result.Pairs, result.Positives, result.Negatives,
		float64(result.EER)*100, result.EERThresh,
		result.Threshold, float64(result.Accuracy)*100,
		result.PosMean, result.PosMin, result.PosMax,
		result.NegMean, result.NegMin, result.NegMax,
		result.Separation)
}

func SuggestThreshold(result EvalResult) float32 {
	if result.Pairs == 0 {
		return 0.45
	}
	suggested := result.EERThresh
	suggested = float32(math.Max(float64(suggested), 0.2))
	suggested = float32(math.Min(float64(suggested), 0.8))
	return suggested
}
