package wt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewCreatesWorktreeAndMeta(t *testing.T) {
	repo := fixtureRepo(t)
	stateDir := t.TempDir()
	m, err := New(stateDir, NewOptions{Task: "auth-api", Repo: repo, NoWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Base != "main" || m.Branch != "agent/auth-api" || m.Agent != "claude" {
		t.Errorf("meta: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(m.Path, "a.txt")); err != nil {
		t.Error("worktree에 파일 있어야 함")
	}
	if !BranchExists(repo, "agent/auth-api") {
		t.Error("브랜치 생성")
	}
	// 같은 태스크 재생성 거부
	if _, err := New(stateDir, NewOptions{Task: "auth-api", Repo: repo, NoWindow: true}); err == nil {
		t.Error("중복 태스크 거부")
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	stateDir := t.TempDir()
	if _, err := New(stateDir, NewOptions{Task: "", Repo: t.TempDir(), NoWindow: true}); err == nil {
		t.Error("빈 태스크 거부")
	}
	if _, err := New(stateDir, NewOptions{Task: "x", Repo: t.TempDir(), NoWindow: true}); err == nil {
		t.Error("git 아닌 폴더 거부")
	}
	repo := fixtureRepo(t)
	if _, err := New(stateDir, NewOptions{Task: "x", Repo: repo, Agent: "gpt9", NoWindow: true}); err == nil {
		t.Error("미지원 에이전트 거부")
	}
}

// 회귀 테스트: task명에 "../"가 있으면 .agentlayer/worktrees 밖으로 경로가
// 탈출해 임의 위치에 worktree를 만들 수 있었다(검증 없음 → CONFIRMED, P0-2).
func TestNewRejectsPathTraversal(t *testing.T) {
	repo := fixtureRepo(t)
	stateDir := t.TempDir()
	for _, bad := range []string{"../outside", "a/../../outside", "..", "a/", "/a"} {
		if _, err := New(stateDir, NewOptions{Task: bad, Repo: repo, NoWindow: true}); err == nil {
			t.Errorf("경로 이동 태스크명 거부돼야 함: %q", bad)
		}
	}
	// 저장소 밖에 아무것도 생성되지 않았는지 확인
	if _, err := os.Stat(filepath.Join(filepath.Dir(repo), "outside")); err == nil {
		t.Error("저장소 밖에 worktree가 생성됨")
	}
}

func TestValidTaskName(t *testing.T) {
	for _, ok := range []string{"auth-api", "feature/login", "a.b_c"} {
		if err := ValidTaskName(ok); err != nil {
			t.Errorf("허용돼야 함 %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", ".", "../x", "x/..", "a//b", "/a"} {
		if err := ValidTaskName(bad); err == nil {
			t.Errorf("거부돼야 함: %q", bad)
		}
	}
}

func TestCleanRefusesDirtyAndUnmerged(t *testing.T) {
	repo := fixtureRepo(t)
	stateDir := t.TempDir()
	m, err := New(stateDir, NewOptions{Task: "t1", Repo: repo, NoWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	// dirty → 거부
	os.WriteFile(filepath.Join(m.Path, "wip.txt"), []byte("작업중\n"), 0o644)
	err = Clean(stateDir, "t1")
	var refusal *CleanRefusal
	if r, ok := err.(*CleanRefusal); !ok {
		t.Fatalf("CleanRefusal이어야 함: %v", err)
	} else {
		refusal = r
	}
	if len(refusal.Dirty) != 1 {
		t.Errorf("dirty 1건: %+v", refusal)
	}
	if _, err := os.Stat(m.Path); err != nil {
		t.Fatal("거부 시 worktree 보존")
	}
	// 커밋 → 미병합 거부
	gitRun(t, m.Path, "add", ".")
	gitRun(t, m.Path, "commit", "-m", "wip")
	err = Clean(stateDir, "t1")
	if r, ok := err.(*CleanRefusal); !ok || r.Unmerged != 1 {
		t.Fatalf("미병합 거부: %v", err)
	}
	// 병합 → 정리 성공
	if err := Merge(m.Repo, m.Base, m.Branch); err != nil {
		t.Fatal(err)
	}
	if err := Clean(stateDir, "t1"); err != nil {
		t.Fatalf("깨끗+병합 후 정리: %v", err)
	}
	if _, err := os.Stat(m.Path); err == nil {
		t.Error("worktree 제거돼야 함")
	}
	if _, err := LoadMeta(stateDir, "t1"); err == nil {
		t.Error("메타 제거돼야 함")
	}
}

// 회귀 테스트: Dirty/Unmerged 조회 자체가 실패하면(예: worktree 폴더가
// 파일시스템에서 사라진 경우) 예전엔 에러를 조용히 무시하고 "깨끗함"으로
// 오판해 그대로 지웠다(CONFIRMED, P0-3) — 이제는 fail-closed로 거부해야 한다.
func TestCleanFailsClosedOnCheckError(t *testing.T) {
	repo := fixtureRepo(t)
	stateDir := t.TempDir()
	m, err := New(stateDir, NewOptions{Task: "broken", Repo: repo, NoWindow: true})
	if err != nil {
		t.Fatal(err)
	}
	// worktree 디렉터리를 직접 지워 git 명령이 실패하게 만든다(수동 삭제·권한
	// 문제 등으로 손상된 worktree를 재현).
	if err := os.RemoveAll(m.Path); err != nil {
		t.Fatal(err)
	}
	err = Clean(stateDir, "broken")
	if err == nil {
		t.Fatal("확인 실패 시 정리를 거부해야 함")
	}
	if _, ok := err.(*CleanRefusal); ok {
		t.Error("확인 실패는 CleanRefusal이 아니라 별도 에러로 구분돼야 함")
	}
	if _, err := LoadMeta(stateDir, "broken"); err != nil {
		t.Error("확인 실패 시 메타도 보존돼야 함(삭제 진행 안 됨)")
	}
}

func TestMergeGuideConfirmFlow(t *testing.T) {
	repo := fixtureRepo(t)
	stateDir := t.TempDir()
	m, _ := New(stateDir, NewOptions{Task: "t2", Repo: repo, NoWindow: true})
	os.WriteFile(filepath.Join(m.Path, "f.txt"), []byte("x\n"), 0o644)
	gitRun(t, m.Path, "add", ".")
	gitRun(t, m.Path, "commit", "-m", "work")

	// n → merge 안 함
	var buf bytes.Buffer
	if err := MergeGuide(&buf, stateDir, "t2", func() bool { return false }); err != nil {
		t.Fatal(err)
	}
	if n, _ := Unmerged(repo, "main", "agent/t2"); n != 1 {
		t.Error("거절 시 merge 안 됨")
	}
	if !strings.Contains(buf.String(), "git -C") || !strings.Contains(buf.String(), "--no-ff") {
		t.Errorf("명령 안내 포함:\n%s", buf.String())
	}
	// y → merge 실행
	buf.Reset()
	if err := MergeGuide(&buf, stateDir, "t2", func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if n, _ := Unmerged(repo, "main", "agent/t2"); n != 0 {
		t.Error("승인 시 merge 완료")
	}
}

func TestCommandFor(t *testing.T) {
	if got := commandFor("claude"); got != "claude" {
		t.Fatalf("claude: got %q", got)
	}
	if got := commandFor("codex"); got != "codex" {
		t.Fatalf("codex: got %q", got)
	}
	// gemini는 agy 흔적 유무로 갈린다 (usage.GeminiCommand 규칙 공유)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := commandFor("gemini"); got != "gemini" {
		t.Fatalf("agy 흔적 없음 = stock 폴백이어야: got %q", got)
	}
	if err := os.MkdirAll(filepath.Join(home, ".gemini", "antigravity-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := commandFor("gemini"); got != "agy" {
		t.Fatalf("antigravity-cli 흔적 있으면 agy여야: got %q", got)
	}
}
