package cli

import (
	"flag"
	"testing"
)

// 회귀 테스트: wt CLI 진입점(oneTask·parseTaskAndFlags)도 wt.New과 동일하게
// "../" 태스크명을 거부해야 한다(P0-2, defense-in-depth).
func TestOneTaskRejectsPathTraversal(t *testing.T) {
	if _, err := oneTask([]string{"../outside"}); err == nil {
		t.Error("경로 이동 태스크명 거부돼야 함")
	}
	if _, err := oneTask([]string{"auth-api"}); err != nil {
		t.Errorf("정상 태스크명은 통과해야 함: %v", err)
	}
}

func TestParseTaskAndFlagsRejectsPathTraversal(t *testing.T) {
	fs := flag.NewFlagSet("wt new", flag.ContinueOnError)
	if _, err := parseTaskAndFlags(fs, []string{"../outside"}); err == nil {
		t.Error("경로 이동 태스크명 거부돼야 함")
	}
}
