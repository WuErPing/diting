package paragraph

import "testing"

func TestApplyCorrectionsAdoptsSmallFix(t *testing.T) {
	result, applied := ApplyCorrections([]string{"在要总结。"}, []string{"摘要总结。"})
	if applied != 1 || result[0] != "摘要总结。" {
		t.Fatalf("result = %v, applied = %d", result, applied)
	}
}

func TestApplyCorrectionsRejectsRewrite(t *testing.T) {
	result, applied := ApplyCorrections([]string{"在要总结。"}, []string{"我们需要对全文做一个摘要。"})
	if applied != 0 || result[0] != "在要总结。" {
		t.Fatalf("result = %v, applied = %d", result, applied)
	}
}

func TestApplyCorrectionsRejectsEmptyAndKeepsIdentical(t *testing.T) {
	result, applied := ApplyCorrections(
		[]string{"在要总结。", "摘要总结。", ""},
		[]string{"  ", "摘要总结。", "凭空生成"},
	)
	if applied != 0 {
		t.Fatalf("applied = %d", applied)
	}
	if result[0] != "在要总结。" || result[1] != "摘要总结。" || result[2] != "" {
		t.Fatalf("result = %v", result)
	}
}

func TestApplyCorrectionsRejectsCountMismatch(t *testing.T) {
	result, applied := ApplyCorrections([]string{"在要总结。", "第二句。"}, []string{"摘要总结。"})
	if applied != 0 || result[0] != "在要总结。" {
		t.Fatalf("result = %v, applied = %d", result, applied)
	}
}

func TestApplyCorrectionsScalesBudgetWithLength(t *testing.T) {
	original := "我会希望对一个主题做拆要总结。"
	result, applied := ApplyCorrections([]string{original}, []string{"我会希望对一个主题做摘要总结。"})
	if applied != 1 || result[0] != "我会希望对一个主题做摘要总结。" {
		t.Fatalf("result = %v, applied = %d", result, applied)
	}
}

func TestApplyCorrectionsTrimsCandidate(t *testing.T) {
	result, applied := ApplyCorrections([]string{"在要总结。"}, []string{" 摘要总结。 "})
	if applied != 1 || result[0] != "摘要总结。" {
		t.Fatalf("result = %v, applied = %d", result, applied)
	}
}
