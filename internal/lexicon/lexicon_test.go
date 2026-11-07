package lexicon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestApplyUsesLongestMatchWithoutCascading(t *testing.T) {
	instance, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Replace([]Rule{
		{From: "张总", To: "章总"},
		{From: "张总办", To: "章总办公室"},
		{From: "章总", To: "不应级联"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := instance.Apply("张总办和张总"); got != "章总办公室和章总" {
		t.Fatalf("mapped = %q", got)
	}
	if got := instance.Apply("没有命中"); got != "没有命中" {
		t.Fatalf("unmapped = %q", got)
	}
}

func TestApplyDeletionRule(t *testing.T) {
	instance, _ := Open("")
	if err := instance.Replace([]Rule{{From: "嗯", To: ""}}); err != nil {
		t.Fatal(err)
	}
	if got := instance.Apply("嗯，你好"); got != "，你好" {
		t.Fatalf("mapped = %q", got)
	}
}

func TestReplacePersistsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lexicon.json")
	instance, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Replace([]Rule{{From: " 张总 ", To: " 章总 "}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rules := reopened.Rules()
	if len(rules) != 1 || rules[0].From != "张总" || rules[0].To != "章总" {
		t.Fatalf("rules = %+v", rules)
	}
	if got := reopened.Apply("张总"); got != "章总" {
		t.Fatalf("mapped = %q", got)
	}
}

func TestValidateRejectsInvalidRules(t *testing.T) {
	cases := [][]Rule{
		{{From: "", To: "章总"}},
		{{From: "张总", To: "张总"}},
		{{From: "张总", To: "章总"}, {From: "张总", To: "王总"}},
		{{From: strings.Repeat("长", maxFromRunes+1), To: "短"}},
		{{From: "张总", To: strings.Repeat("长", maxToRunes+1)}},
		{{From: "张\n总", To: "章总"}},
	}
	for _, rules := range cases {
		if err := Validate(rules); !errors.Is(err, ErrInvalidRule) {
			t.Fatalf("rules %+v: err = %v", rules, err)
		}
	}
	tooMany := make([]Rule, maxRules+1)
	for i := range tooMany {
		tooMany[i] = Rule{From: "错" + string(rune('a'+i)), To: "对"}
	}
	if err := Validate(tooMany); !errors.Is(err, ErrInvalidRule) {
		t.Fatalf("too many rules: %v", err)
	}
}

func TestConcurrentApplyAndReplace(t *testing.T) {
	instance, _ := Open("")
	if err := instance.Replace([]Rule{{From: "张总", To: "章总"}}); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < 100; j++ {
				if got := instance.Apply("张总"); got != "章总" {
					t.Errorf("mapped = %q", got)
					return
				}
			}
		}()
	}
	if err := instance.Replace([]Rule{{From: "张总", To: "章总"}}); err != nil {
		t.Fatal(err)
	}
	wait.Wait()
}
