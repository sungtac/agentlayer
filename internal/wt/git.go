package wt

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoRoot는 dir이 속한 저장소 루트를 돌려준다.
func RepoRoot(dir string) (string, error) {
	return git(dir, "rev-parse", "--show-toplevel")
}

// BranchExists는 로컬 브랜치 존재 여부.
func BranchExists(repo, branch string) bool {
	_, err := git(repo, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// AddWorktree는 base에서 branch를 새로 따 path에 worktree를 만든다.
func AddWorktree(repo, base, branch, path string) error {
	_, err := git(repo, "worktree", "add", "-b", branch, path, base)
	return err
}

// Dirty는 worktree의 미커밋·untracked 파일 목록.
func Dirty(path string) ([]string, error) {
	out, err := git(path, "status", "--porcelain", "-uall")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// Unmerged는 base에 아직 병합되지 않은 branch 커밋 수.
func Unmerged(repo, base, branch string) (int, error) {
	out, err := git(repo, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// Diff는 base 대비 worktree의 변경 전체(커밋된 것 + 워킹트리)를 돌려준다.
// untracked 파일은 목록으로 덧붙인다.
func Diff(path, base string) (string, error) {
	diff, err := git(path, "diff", base)
	if err != nil {
		return "", err
	}
	out, _ := git(path, "ls-files", "--others", "--exclude-standard")
	if out != "" {
		diff += "\n# untracked 파일:\n"
		for _, f := range strings.Split(out, "\n") {
			diff += "#   " + f + "\n"
		}
	}
	return diff, nil
}

// RemoveWorktree는 worktree를 제거한다. 안전 검사는 호출자(Clean) 책임 —
// 이 함수는 --force를 절대 쓰지 않으므로 git 자체의 보호도 유지된다.
func RemoveWorktree(repo, path string) error {
	if _, err := git(repo, "worktree", "remove", path); err != nil {
		return err
	}
	_, _ = git(repo, "worktree", "prune")
	return nil
}

// DeleteBranch는 병합된 브랜치만 지운다 (-d, -D 아님).
func DeleteBranch(repo, branch string) error {
	_, err := git(repo, "branch", "-d", branch)
	return err
}

// Merge는 repo의 base 브랜치에 branch를 --no-ff로 병합한다.
// 호출 전 확인(MergeGuide)을 거쳤다는 전제. 충돌 시 git 메시지 그대로 반환.
//
// 메인 저장소가 이미 base에 있으면 그 자리에서 병합한다(사용자가 인지한
// 상태이므로 일반 git 워크플로와 동일 — 충돌 시 사용자가 직접 해결). base가
// 아닌 다른 브랜치에 있으면 예전엔 무조건 `checkout base`부터 해서 사용자의
// 현재 브랜치·미커밋 작업을 침범했다 — 대신 임시 worktree를 만들어 그 안에서
// 병합한다(git은 같은 브랜치를 두 worktree에서 동시에 체크아웃할 수 없어,
// base가 여기 살아있는 동안은 이 방식이 메인 체크아웃을 전혀 안 건드리는
// 유일한 경로다). 충돌 시 임시 worktree를 지우지 않고 경로를 알려준다.
func Merge(repo, base, branch string) error {
	cur, err := git(repo, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("현재 브랜치 확인 실패: %w", err)
	}
	if cur == base {
		_, err := git(repo, "merge", "--no-ff", branch, "-m",
			fmt.Sprintf("Merge %s (agentlayer wt)", branch))
		return err
	}
	tmpWT, err := os.MkdirTemp("", "agentlayer-merge-*")
	if err != nil {
		return err
	}
	if _, err := git(repo, "worktree", "add", tmpWT, base); err != nil {
		os.RemoveAll(tmpWT)
		return fmt.Errorf("병합용 임시 worktree 생성 실패: %w", err)
	}
	if _, err := git(tmpWT, "merge", "--no-ff", branch, "-m",
		fmt.Sprintf("Merge %s (agentlayer wt)", branch)); err != nil {
		return fmt.Errorf("merge 충돌 — 현재 브랜치(%s)는 건드리지 않았습니다. %s 에서 직접 해결한 뒤 'git -C %s worktree remove %s'로 정리하세요: %w",
			cur, tmpWT, repo, tmpWT, err)
	}
	if _, err := git(repo, "worktree", "remove", tmpWT); err != nil {
		return fmt.Errorf("병합은 %s로 성공했지만 임시 worktree 정리 실패(%s): %w", base, tmpWT, err)
	}
	return nil
}
