package speaker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTestSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testset.jsonl")
	content := `{"audio":"a.wav","speaker":"Andy"}
{"audio":"b.wav","speaker":"Andy"}
{"audio":"c.wav","speaker":"Bob"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadTestSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].Audio != "a.wav" || entries[0].Speaker != "Andy" {
		t.Fatalf("entry[0] = %+v", entries[0])
	}
	if entries[2].Speaker != "Bob" {
		t.Fatalf("entry[2].Speaker = %q", entries[2].Speaker)
	}
}

func TestLoadTestSetErrors(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.jsonl")
	if _, err := LoadTestSet(missing); err == nil {
		t.Fatal("expected error for missing file")
	}

	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, []byte(`{"audio":"a.wav","speaker":"X"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTestSet(empty); err == nil {
		t.Fatal("expected error for single entry")
	}

	bad := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTestSet(bad); err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	noFields := filepath.Join(dir, "nofields.jsonl")
	if err := os.WriteFile(noFields, []byte("{\"audio\":\"\",\"speaker\":\"\"}\n{\"audio\":\"x\",\"speaker\":\"y\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTestSet(noFields); err == nil {
		t.Fatal("expected error for empty audio/speaker")
	}
}

func TestLoadTestSetWithOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testset.jsonl")
	content := `{"audio":"a.wav","speaker":"Andy","offset":1.5,"limit":3.0}
{"audio":"b.wav","speaker":"Bob"}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadTestSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Offset != 1.5 || entries[0].Limit != 3.0 {
		t.Fatalf("offset/limit = %f/%f", entries[0].Offset, entries[0].Limit)
	}
	if entries[1].Offset != 0 || entries[1].Limit != 0 {
		t.Fatalf("default offset/limit = %f/%f", entries[1].Offset, entries[1].Limit)
	}
}

func TestEvaluatePerfectSeparation(t *testing.T) {
	embeddings := [][]float32{
		{1, 0, 0, 0},
		{0.99, 0.01, 0, 0},
		{0, 1, 0, 0},
		{0, 0.99, 0.01, 0},
	}
	labels := []string{"A", "A", "B", "B"}

	result := Evaluate(embeddings, labels, 0.5)
	if result.Total != 4 || result.Speakers != 2 {
		t.Fatalf("total=%d speakers=%d", result.Total, result.Speakers)
	}
	if result.Positives != 2 || result.Negatives != 4 {
		t.Fatalf("pos=%d neg=%d", result.Positives, result.Negatives)
	}
	if result.Accuracy != 1.0 {
		t.Fatalf("accuracy = %f, want 1.0", result.Accuracy)
	}
	if result.EER != 0 {
		t.Fatalf("EER = %f, want 0", result.EER)
	}
	if result.Separation <= 0 {
		t.Fatalf("separation = %f, want > 0", result.Separation)
	}
}

func TestEvaluateOverlapping(t *testing.T) {
	embeddings := [][]float32{
		{1, 0, 0},
		{0, 1, 0},
		{0.7, 0.7, 0},
	}
	labels := []string{"A", "B", "A"}

	result := Evaluate(embeddings, labels, 0.5)
	if result.Pairs != 3 {
		t.Fatalf("pairs = %d, want 3", result.Pairs)
	}
	if result.Accuracy < 0 || result.Accuracy > 1 {
		t.Fatalf("accuracy = %f out of range", result.Accuracy)
	}
}

func TestEvaluateSingleSpeaker(t *testing.T) {
	embeddings := [][]float32{{1, 0}, {0.9, 0.1}}
	labels := []string{"A", "A"}

	result := Evaluate(embeddings, labels, 0.5)
	if result.Negatives != 0 {
		t.Fatalf("negatives = %d, want 0", result.Negatives)
	}
	if result.Pairs != 1 {
		t.Fatalf("pairs = %d, want 1", result.Pairs)
	}
}

func TestEvaluateInvalidEmbeddings(t *testing.T) {
	embeddings := [][]float32{{0, 0}, {1, 0}, {0.95, 0.05}, {0, 1}}
	labels := []string{"A", "A", "A", "B"}

	result := Evaluate(embeddings, labels, 0.5)
	if result.Positives != 1 {
		t.Fatalf("positives = %d, want 1 (zero embedding pairs skipped)", result.Positives)
	}
	if result.Negatives != 2 {
		t.Fatalf("negatives = %d, want 2", result.Negatives)
	}
}

func TestEvaluateMismatchedLengths(t *testing.T) {
	result := Evaluate([][]float32{{1, 0}}, []string{"A", "B"}, 0.5)
	if result.Pairs != 0 {
		t.Fatalf("mismatched lengths should produce empty result")
	}
}

func TestComputeEER(t *testing.T) {
	positives := []float32{0.9, 0.85, 0.8, 0.75, 0.7}
	negatives := []float32{0.3, 0.25, 0.2, 0.15, 0.1}

	eer, thresh := computeEER(positives, negatives)
	if eer != 0 {
		t.Fatalf("EER = %f, want 0 for perfectly separated scores", eer)
	}
	if thresh < 0.3 || thresh > 0.7 {
		t.Fatalf("threshold = %f, want between 0.3 and 0.7", thresh)
	}
}

func TestComputeEEROverlapping(t *testing.T) {
	positives := []float32{0.8, 0.6, 0.4}
	negatives := []float32{0.7, 0.5, 0.3}

	eer, _ := computeEER(positives, negatives)
	if eer <= 0 || eer >= 0.5 {
		t.Fatalf("EER = %f, want between 0 and 0.5 for overlapping", eer)
	}
}

func TestSuggestThreshold(t *testing.T) {
	result := EvalResult{PosMean: 0.8, NegMean: 0.3, Pairs: 10, EERThresh: 0.612}
	if got := SuggestThreshold(result); got != 0.612 {
		t.Fatalf("suggested = %f, want EER threshold 0.612", got)
	}

	empty := EvalResult{}
	if got := SuggestThreshold(empty); got != 0.45 {
		t.Fatalf("empty suggest = %f, want 0.45", got)
	}
}

func TestFormatResult(t *testing.T) {
	empty := EvalResult{}
	if got := FormatResult(empty); got != "无法评估：没有有效的配对数据" {
		t.Fatalf("empty format = %q", got)
	}

	result := EvalResult{
		Total: 10, Speakers: 3, Pairs: 45, Positives: 15, Negatives: 30,
		EER: 0.05, EERThresh: 0.6, Accuracy: 0.9, Threshold: 0.45,
		PosMean: 0.8, PosMin: 0.6, PosMax: 0.95,
		NegMean: 0.3, NegMin: 0.1, NegMax: 0.5,
		Separation: 0.5,
	}
	output := FormatResult(result)
	if len(output) == 0 {
		t.Fatal("format produced empty output")
	}
}

func TestClassify(t *testing.T) {
	positives := []float32{0.8, 0.6, 0.3}
	negatives := []float32{0.7, 0.2, 0.1}

	tp, fp, tn, fn := classify(positives, negatives, 0.5)
	if tp != 2 || fn != 1 {
		t.Fatalf("tp=%d fn=%d, want 2/1", tp, fn)
	}
	if fp != 1 || tn != 2 {
		t.Fatalf("fp=%d tn=%d, want 1/2", fp, tn)
	}
}
