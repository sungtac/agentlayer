package wt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixtureRepo는 커밋 하나 있는 임시 git repo를 만든다.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestWorktreeLifecycleChecks(t *testing.T) {
	repo := fixtureRepo(t)
	wtPath := filepath.Join(t.TempDir(), "task1")

	if err := AddWorktree(repo, "main", "agent/task1", wtPath); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(repo, "agent/task1") {
		t.Error("브랜치 생성돼야 함")
	}

	// 깨끗한 상태
	d, err := Dirty(wtPath)
	if err != nil || len(d) != 0 {
		t.Errorf("깨끗: %v %v", d, err)
	}
	n, err := Unmerged(repo, "main", "agent/task1")
	if err != nil || n != 0 {
		t.Errorf("미병합 0: %d %v", n, err)
	}

	// 파일 수정 → dirty + diff
	if err := os.WriteFile(filepath.Join(wtPath, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wtPath, "new.txt"), []byte("신규\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ = Dirty(wtPath)
	if len(d) != 2 {
		t.Errorf("dirty 2건: %v", d)
	}
	diff, err := Diff(wtPath, "main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "changed") || !strings.Contains(diff, "untracked") || !strings.Contains(diff, "new.txt") {
		t.Errorf("diff 내용:\n%s", diff)
	}

	// dirty 상태에서 RemoveWorktree는 git이 거부해야 함 (--force 안 쓰므로)
	if err := RemoveWorktree(repo, wtPath); err == nil {
		t.Fatal("dirty worktree 제거는 거부돼야 함")
	}

	// 커밋 → unmerged 1
	gitRun(t, wtPath, "add", ".")
	gitRun(t, wtPath, "commit", "-m", "work")
	n, _ = Unmerged(repo, "main", "agent/task1")
	if n != 1 {
		t.Errorf("미병합 1: %d", n)
	}

	// merge 후 정리 가능
	if err := Merge(repo, "main", "agent/task1"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(repo, wtPath); err != nil {
		t.Fatalf("깨끗+병합됨 제거: %v", err)
	}
	if err := DeleteBranch(repo, "agent/task1"); err != nil {
		t.Fatalf("병합된 브랜치 삭제: %v", err)
	}
}

// 회귀 테스트: 메인 저장소가 base가 아닌 다른 브랜치에 있을 때 Merge가 그
// 브랜치를 그대로 두는지 확인한다(P1-4) — 예전엔 무조건 `checkout base`부터
// 해서 사용자의 현재 브랜치를 임의로 바꿔버렸다.
func TestMergeDoesNotDisturbCurrentBranchWhenElsewhere(t *testing.T) {
	repo := fixtureRepo(t)
	wtPath := filepath.Join(t.TempDir(), "task2")
	if err := AddWorktree(repo, "main", "agent/task2", wtPath); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wtPath, "commit", "--allow-empty", "-m", "work")

	// 사용자가 메인 저장소에서 다른 브랜치로 작업 중이라고 가정
	gitRun(t, repo, "checkout", "-b", "scratch")
	os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("작업중\n"), 0o644)

	if err := Merge(repo, "main", "agent/task2"); err != nil {
		t.Fatal(err)
	}
	// 메인 저장소는 여전히 scratch, 미커밋 파일도 그대로여야 함
	if cur, _ := git(repo, "branch", "--show-current"); cur != "scratch" {
		t.Errorf("현재 브랜치가 바뀌면 안 됨: got %q", cur)
	}
	if _, err := os.Stat(filepath.Join(repo, "wip.txt")); err != nil {
		t.Error("미커밋 파일이 보존돼야 함")
	}
	// main은 실제로 병합됐어야 함
	if n, _ := Unmerged(repo, "main", "agent/task2"); n != 0 {
		t.Error("main 브랜치에 병합이 반영돼야 함")
	}
}

// 회귀 테스트: 충돌이 나도 사용자의 현재 브랜치는 건드리지 않아야 한다.
func TestMergeConflictLeavesCurrentBranchIntact(t *testing.T) {
	repo := fixtureRepo(t)
	wtPath := filepath.Join(t.TempDir(), "task3")
	if err := AddWorktree(repo, "main", "agent/task3", wtPath); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wtPath, "a.txt"), []byte("from branch\n"), 0o644)
	gitRun(t, wtPath, "commit", "-am", "branch change")

	gitRun(t, repo, "checkout", "-b", "scratch")
	gitRun(t, repo, "checkout", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("from main\n"), 0o644)
	gitRun(t, repo, "commit", "-am", "main change")
	gitRun(t, repo, "checkout", "scratch")

	if err := Merge(repo, "main", "agent/task3"); err == nil {
		t.Fatal("충돌이 있으면 실패해야 함")
	}
	if cur, _ := git(repo, "branch", "--show-current"); cur != "scratch" {
		t.Errorf("충돌 시에도 현재 브랜치가 바뀌면 안 됨: got %q", cur)
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

func TestMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Truncate(time.Second)
	m := &Meta{Task: "auth-api", Repo: "/r", Base: "main", Branch: "agent/auth-api",
		Path: "/w", Agent: "claude", TestCmd: "go test ./...", CreatedAt: now}
	if err := SaveMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	back, err := LoadMeta(dir, "auth-api")
	if err != nil {
		t.Fatal(err)
	}
	if back.Branch != m.Branch || back.Agent != "claude" {
		t.Errorf("round-trip: %+v", back)
	}
	list, _ := ListMetas(dir)
	if len(list) != 1 {
		t.Errorf("list: %d", len(list))
	}
	if err := DeleteMeta(dir, "auth-api"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMeta(dir, "auth-api"); err == nil {
		t.Error("삭제 후 로드 실패해야 함")
	}
}

func TestListMetasEmpty(t *testing.T) {
	list, err := ListMetas(t.TempDir())
	if err != nil || list != nil {
		t.Errorf("빈 목록: %v %v", list, err)
	}
}
