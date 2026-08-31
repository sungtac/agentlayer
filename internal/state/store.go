package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// DefaultDir는 상태 정본 디렉터리. AGENTLAYER_STATE_DIR로 오버라이드 가능.
func DefaultDir() string {
	if d := os.Getenv("AGENTLAYER_STATE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "agentlayer")
	}
	return filepath.Join(home, ".local", "state", "agentlayer")
}

// Store는 디렉터리 기반 상태 저장소. 에이전트 1개 = agents/<id>.json 1개.
// 데몬 없이 여러 프로세스(hook, TUI, CLI)가 동시에 써도 temp→rename
// 원자적 쓰기로 파일이 깨지지 않는다.
type Store struct {
	Dir string
}

func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o755); err != nil {
		return nil, fmt.Errorf("상태 디렉터리 생성: %w", err)
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(id string) string {
	// ID가 경로 구분자를 품어도 파일명 하나로 남게 한다.
	safe := strings.NewReplacer("/", "_", string(filepath.Separator), "_").Replace(id)
	return filepath.Join(s.Dir, "agents", safe+".json")
}

// withLock은 id 하나에 대한 배타적 파일 잠금(flock) 안에서 fn을 실행한다.
// temp→rename은 파일 손상만 막을 뿐 read-modify-write 유실은 막지 못한다 —
// 예: TUI의 MarkRead가 레코드를 읽는 사이 hook이 새 상태를 저장하면, 오래된
// MarkRead 스냅샷이 나중에 그 상태를 덮어쓸 수 있다. 같은 id를 다루는
// Load+Save 시퀀스(Update·MarkRead)를 이 잠금으로 감싸 그 경쟁을 없앤다.
func (s *Store) withLock(id string, fn func() error) error {
	f, err := os.OpenFile(s.path(id)+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}

// Save는 레코드를 원자적으로 기록한다. 동시 Save·Update(MarkRead 등)와의
// 경쟁을 막기 위해 id 단위 잠금으로 감싼다.
func (s *Store) Save(a *Agent) error {
	return s.withLock(a.ID, func() error { return s.saveLocked(a) })
}

// saveLocked는 잠금을 이미 쥔 상태에서만 호출한다(Update 내부 등).
func (s *Store) saveLocked(a *Agent) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(s.Dir, "agents"), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path(a.ID))
}

// Update는 id의 현재 레코드를 잠금 안에서 읽고(없으면 existing=nil) fn에
// 넘긴다. fn이 non-nil Agent를 돌려주면 같은 잠금 안에서 저장한다 — Load와
// Save 사이에 다른 프로세스의 Update·Save가 끼어들 수 없다. hook 어댑터
// (RunClaude 등)와 MarkRead가 이 하나의 통로로 상태를 갱신해야 서로의
// 쓰기를 잃어버리지 않는다.
func (s *Store) Update(id string, fn func(existing *Agent) (*Agent, error)) error {
	return s.withLock(id, func() error {
		existing, _ := s.Load(id) // 없으면 nil — fn이 신규 레코드를 만들지 결정
		next, err := fn(existing)
		if err != nil || next == nil {
			return err
		}
		return s.saveLocked(next)
	})
}

func (s *Store) Load(id string) (*Agent, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var a Agent
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("레코드 파싱 %s: %w", id, err)
	}
	return &a, nil
}

// List는 전체 레코드를 Priority → StateSince 순으로 반환한다.
// 깨진 파일은 건너뛴다 — 관제탑은 일부가 깨져도 나머지를 보여줘야 한다.
func (s *Store) List() ([]*Agent, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "agents"))
	if err != nil {
		return nil, err
	}
	var out []*Agent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Dir, "agents", e.Name()))
		if err != nil {
			continue
		}
		var a Agent
		if err := json.Unmarshal(b, &a); err != nil {
			continue
		}
		out = append(out, &a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		// 3사 정보가 섞이지 않게 종류 그룹 먼저, 그룹 안에서 급한 순
		ki, kj := KindRank(out[i].Kind), KindRank(out[j].Kind)
		if ki != kj {
			return ki < kj
		}
		pi, pj := out[i].State.Priority(), out[j].State.Priority()
		if pi != pj {
			return pi < pj
		}
		return out[i].StateSince.Before(out[j].StateSince)
	})
	return out, nil
}

// MarkRead는 DONE_UNREAD를 IDLE로 바꾼다. 다른 상태면 아무것도 안 한다 —
// 읽음 처리는 사용자 행동이며 다른 전이를 덮어쓰면 안 된다. Update를 통해
// 읽기·조건 확인·쓰기가 하나의 잠금 안에서 일어나므로, 그 사이 hook이 새
// 상태를 저장해도 유실되지 않는다(그 hook도 Update를 거친다는 전제).
func (s *Store) MarkRead(id string, now time.Time) error {
	return s.Update(id, func(a *Agent) (*Agent, error) {
		if a == nil || a.State != StateDoneUnread {
			return nil, nil
		}
		a.Transition(StateIdle, now)
		return a, nil
	})
}

func (s *Store) Delete(id string) error {
	return os.Remove(s.path(id))
}
