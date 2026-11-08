package engine

import "testing"

func TestReconcileTranscriptCorrectsSingleCharacterSubstitution(t *testing.T) {
	got := reconcileTranscript("这额都又快用完了。", "这额度又快用完了")
	if got != "这额度又快用完了。" {
		t.Fatalf("text = %q", got)
	}
}

func TestReconcileTranscriptCorrectsSingleCharacterInsertion(t *testing.T) {
	got := reconcileTranscript("这额度快用完了。", "这额度又快用完了")
	if got != "这额度又快用完了。" {
		t.Fatalf("text = %q", got)
	}
}

func TestReconcileTranscriptRejectsTwoCharacterDifference(t *testing.T) {
	offline := "这都又快用完了。"
	got := reconcileTranscript(offline, "这额度又快用完了")
	if got != offline {
		t.Fatalf("text = %q, want offline text", got)
	}
}

func TestReconcileTranscriptUsesAgreedStreamingModelsForTwoDifferences(t *testing.T) {
	got := reconcileTranscript("这耳都又快用完了。", "这额度又快用完了", "这额度又快用完了")
	if got != "这额度又快用完了。" {
		t.Fatalf("text = %q", got)
	}
}

func TestReconcileTranscriptRejectsTwoDifferencesWhenStreamingModelsDisagree(t *testing.T) {
	offline := "这耳都又快用完了。"
	got := reconcileTranscript(offline, "这额度又快用完了", "这耳度又快用完了")
	if got != offline {
		t.Fatalf("text = %q, want offline text", got)
	}
}

func TestReconcileTranscriptKeepsOfflineTextWhenModelsDisagree(t *testing.T) {
	offline := "这句话的识别结果不同。"
	got := reconcileTranscript(offline, "这句话识别结果也不一样")
	if got != offline {
		t.Fatalf("text = %q, want offline text", got)
	}
}

func TestReconcileTranscriptKeepsInternalPunctuation(t *testing.T) {
	offline := "这句话，有内部标点。"
	got := reconcileTranscript(offline, "这句活有内部标点")
	if got != offline {
		t.Fatalf("text = %q, want offline text", got)
	}
}

func TestReconcileTranscriptKeepsMatchingOfflineText(t *testing.T) {
	offline := "结果相同，保留标点。"
	online := "结果相同，保留标点。"
	if got := reconcileTranscript(offline, online); got != offline {
		t.Fatalf("text = %q", got)
	}
}
