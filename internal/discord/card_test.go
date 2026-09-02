package discord

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netwaif/agentlayer/internal/starter"
	"github.com/netwaif/agentlayer/internal/state"
	"github.com/netwaif/agentlayer/internal/usage"
)

var t0 = time.Date(2026, 8, 25, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))

func pf(v float64) *float64 { return &v }

func fixturePayload() *usage.Payload {
	return &usage.Payload{Providers: map[string]usage.Provider{
		"claude": {OK: true, Plan: "Max", Email: "kshxxthm@gmail.com", Level: "green",
			Action: "지금 큰 작업 돌리세요", Reason: "여유 있어요.",
			Windows: map[string]usage.Window{
				"5h": {LeftPct: pf(77), ResetMin: pf(197)},
				"7d": {LeftPct: pf(16), ResetMin: pf(7)}}},
		"antigravity": {OK: true, Email: "know@x.com", Level: "green", Action: "OK",
			Windows: map[string]usage.Window{
				"knowhackking": {LeftPct: pf(64)}, "aitipking": {}}},
	}}
}

func fixtureData() CardData {
	agents := []*state.Agent{
		{ID: "claude-7", Kind: "claude", State: state.StateWaiting, Task: "승인 대기",
			Tmux: state.TmuxRef{Session: "collab-bot"}, CWD: "/Users/soonho/ai-folder/collab",
			StateSince: t0.Add(-8 * time.Minute), UpdatedAt: t0.Add(-8 * time.Minute)},
		// WORK인데 갱신이 오래 끊김 → 정체 의심 "작업중?"
		{ID: "codex-3", Kind: "codex", State: state.StateWorking,
			Tmux: state.TmuxRef{Session: "codex-bridge"}, CWD: "/Users/soonho/bridge",
			StateSince: t0.Add(-2 * time.Hour), UpdatedAt: t0.Add(-2 * time.Hour)},
		{ID: "claude-9", Kind: "claude", State: state.StateDead,
			CWD: "/Users/soonho/gone", StateSince: t0.Add(-time.Hour)},
	}
	// 에이전트 ID 키 — 같은 폴더의 다른 종류 에이전트와 오귀속되지 않게
	ctx := map[string]usage.CtxInfo{
		"claude-7": {Model: "Opus 5 (1M context)", UsedPct: pf(16), TS: t0.Add(-3 * time.Minute)},
	}
	return CardData{
		Pay:    fixturePayload(),
		Agents: agents,
		Ctx:    ctx,
		Wired:  map[string]string{"/Users/soonho/ai-folder/collab": "⌁collab방"},
		Branches: map[string]string{
			"/Users/soonho/ai-folder/collab": "agent/fix-card"},
		DefModels: map[string]string{"claude": "claude-fable-5", "codex": "gpt-5.6-sol"},
		Tasks:     []starter.Task{{Name: "hwpx-tag", Status: "진행중"}},
		Home:      "/Users/soonho",
	}
}

func TestBuildCard(t *testing.T) {
	d := fixtureData()
	comps := BuildCard(d, t0)
	b, err := json.Marshal(comps)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{
		"Claude — 지금 큰 작업 돌리세요", "kshxxthm@gmail.com",
		"5h", "77", "리셋 3시간 후",
		"knowhackking", // antigravity 계정 행
		"### 에이전트", "~/ai-folder/collab", "Opus 5 (1M context)", "응답 필요", "8분",
		"갱신 \\u003ct:", // json.Marshal이 <를 이스케이프 — Discord는 정상 해석
		"⌁collab방",
		// TUI 동등 정보
		"collab-bot",              // tmux 세션 이름
		"승인 대기",                 // TASK
		"⎇ agent/fix-card",        // worktree 브랜치
		"작업중?",                  // WORK 정체 의심
		"ctx 16%",                 // 게이지 대신 텍스트
		"3분",                     // ctx 스냅샷 나이
		"응답 필요 1",              // 상태 집계 요약
		"기본모델",                 // 기본모델 라인
		"⚠",                       // claude 기본이 Fable → 경고
		"gpt-5.6-sol",             // codex 기본모델
		"Gemini 자동",              // 미설정 = 자동
		"MultiAgent", "hwpx-tag(진행중)",
		"── claude", "── codex", // 종류 그룹 구분선
	} {
		if !strings.Contains(out, want) {
			t.Errorf("카드에 %q 있어야 함", want)
		}
	}
	if strings.Contains(out, "/Users/soonho/gone") {
		t.Error("DEAD 에이전트는 카드에서 제외")
	}
	// 컨테이너 구조 확인
	first := comps[0].(map[string]any)
	if first["type"] != typeContainer || first["accent_color"] == nil {
		t.Errorf("컨테이너 형식: %+v", first)
	}
}

// 에이전트 행에는 게이지 막대를 그리지 않는다 — Discord 폰트에서 격자로
// 깨져 보이고, TUI도 행에는 "ctx N%" 텍스트만 쓴다 (게이지는 provider 창 전용).
func TestBuildCardAgentRowsHaveNoGauge(t *testing.T) {
	d := fixtureData()
	d.Pay = nil // provider 컨테이너 제외하고 에이전트 섹션만
	b, _ := json.Marshal(BuildCard(d, t0))
	out := string(b)
	if strings.Contains(out, "█") || strings.Contains(out, "░") {
		t.Error("에이전트 섹션에 게이지 막대가 있으면 안 됨")
	}
	if !strings.Contains(out, "### 에이전트") {
		t.Error("coach 없이도 에이전트 섹션은 나옴")
	}
}

func TestWorsenedPings(t *testing.T) {
	pay := fixturePayload()
	// green → 첫 관찰: 핑 없음
	pings, lv := WorsenedPings(pay, map[string]string{})
	if len(pings) != 0 {
		t.Errorf("첫 관찰 핑 없음: %v", pings)
	}
	// green → red 악화: 핑
	p := pay.Providers["claude"]
	p.Level = "red"
	p.Action = "미루세요"
	pay.Providers["claude"] = p
	pings, lv = WorsenedPings(pay, lv)
	if len(pings) != 1 || !strings.Contains(pings[0], "Claude") {
		t.Errorf("악화 핑 1건: %v", pings)
	}
	// red 유지: 중복 핑 없음
	pings, _ = WorsenedPings(pay, lv)
	if len(pings) != 0 {
		t.Errorf("유지 상태는 무음: %v", pings)
	}
}

// 회귀 테스트: task/session/branch명에 "@everyone"·백틱 등이 섞여도 실제
// 알림이 발사되거나 카드 서식이 깨지면 안 된다(P1-6). allowed_mentions는
// Discord API 계약이라 여기서는 요청 payload에 실제로 들어가는지 확인하고,
// 이스케이프는 BuildCard 출력에서 확인한다.
func TestUpsertSendsAllowedMentionsBlockingParse(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		fmt.Fprint(w, `{"id":"1"}`)
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL).Upsert([]any{}, ""); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	am, ok := payload["allowed_mentions"].(map[string]any)
	if !ok {
		t.Fatalf("allowed_mentions 없음: %s", body)
	}
	parse, _ := am["parse"].([]any)
	if len(parse) != 0 {
		t.Errorf("parse는 빈 배열(멘션 전부 차단)이어야 함: %v", am)
	}
}

func TestPingSendsAllowedMentionsBlockingParse(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	if err := NewClient(srv.URL).Ping("@everyone red"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"allowed_mentions"`) {
		t.Errorf("Ping도 allowed_mentions을 보내야 함: %s", body)
	}
}

// task·session·branch명에 백틱이 섞여도 카드 서식(코드 스팬·볼드)이 깨지지
// 않아야 한다. BuildCard가 돌려주는 Go 구조체를 그대로 검사한다(JSON
// 인코딩은 백슬래시를 다시 이스케이프해 혼동을 주므로 우회).
func TestBuildCardEscapesMarkdownInDisplayFields(t *testing.T) {
	d := fixtureData()
	d.Pay = nil
	d.Agents[0].Task = "완료`rm -rf /`했음"
	d.Agents[0].Tmux.Session = "sess`x`"
	d.Branches[d.Agents[0].CWD] = "feat`x`"
	comps := BuildCard(d, t0)
	var all strings.Builder
	for _, c := range comps {
		m := c.(map[string]any)
		if content, ok := m["content"].(string); ok {
			all.WriteString(content + "\n")
		}
		if subs, ok := m["components"].([]any); ok {
			for _, sub := range subs {
				sm := sub.(map[string]any)
				all.WriteString(sm["content"].(string) + "\n")
			}
		}
	}
	out := all.String()
	if strings.Contains(out, "`rm -rf /`") {
		t.Errorf("task의 백틱이 이스케이프 안 됨:\n%s", out)
	}
	if strings.Contains(out, "sess`x`") {
		t.Errorf("session의 백틱이 이스케이프 안 됨:\n%s", out)
	}
	if strings.Contains(out, "feat`x`") {
		t.Errorf("branch의 백틱이 이스케이프 안 됨:\n%s", out)
	}
	if !strings.Contains(out, "\\`") {
		t.Errorf("이스케이프된 백틱(\\`)이 안 보임:\n%s", out)
	}
}

func TestUpsertPatchThenPost(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPatch {
			w.WriteHeader(404) // 메시지 삭제됨 가정
			return
		}
		fmt.Fprint(w, `{"id":"999"}`)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	id, err := c.Upsert([]any{}, "123")
	if err != nil {
		t.Fatal(err)
	}
	if id != "999" {
		t.Errorf("404 후 새 POST id: %s", id)
	}
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "PATCH") || !strings.HasPrefix(calls[1], "POST") {
		t.Errorf("PATCH→POST 순서: %v", calls)
	}
}

func TestUpsertPatchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	id, err := NewClient(srv.URL).Upsert([]any{}, "123")
	if err != nil || id != "123" {
		t.Errorf("PATCH 성공 시 기존 id 유지: %s, %v", id, err)
	}
}

// 회귀 테스트: 최초 관찰(last가 비어있음)인데 provider가 이미 red/yellow면
// 예전엔 비교 기준이 없다는 이유로 핑이 아예 안 나갔다(P1-7) — 카드 상태
// 파일이 유실·초기화된 직후 실제로 위험한 상태를 놓칠 수 있었다.
func TestWorsenedPingsFirstObservationAlreadyRed(t *testing.T) {
	pay := fixturePayload()
	p := pay.Providers["claude"]
	p.Level = "red"
	pay.Providers["claude"] = p
	pings, lv := WorsenedPings(pay, map[string]string{})
	if len(pings) != 1 || !strings.Contains(pings[0], "Claude") {
		t.Fatalf("최초 관찰이 이미 red면 핑 1건 나가야 함: %v", pings)
	}
	if lv["claude"] != "red" {
		t.Errorf("level 기록: %v", lv)
	}
	// 같은 레벨 유지 시 중복 핑 없음(기존 동작 보존)
	pings, _ = WorsenedPings(pay, lv)
	if len(pings) != 0 {
		t.Errorf("유지 상태는 무음: %v", pings)
	}
}

func TestCardStateRoundTrip(t *testing.T) {
	p := CardStatePath(t.TempDir())
	if err := SaveCardState(p, &CardState{MessageID: "42", LastLevels: map[string]string{"claude": "green"}}); err != nil {
		t.Fatal(err)
	}
	s := LoadCardState(p)
	if s.MessageID != "42" || s.LastLevels["claude"] != "green" {
		t.Errorf("round-trip: %+v", s)
	}
}

// 회귀 테스트: 여러 프로세스가 동시에 CardState를 read-modify-write하면
// 잠금 없이는 마지막 저장자가 다른 프로세스의 증가분을 덮어써 유실된다
// (P1-7). WithCardStateLock으로 감싸면 N번의 "+1" 갱신이 전부 반영돼야
// 한다 — `go test -race`로 데이터 경쟁도 함께 검증.
func TestWithCardStateLockSerializesConcurrentUpdates(t *testing.T) {
	p := CardStatePath(t.TempDir())
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := WithCardStateLock(p, func(cs *CardState) (*CardState, error) {
				if cs.LastLevels == nil {
					cs.LastLevels = map[string]string{}
				}
				cnt := 0
				fmt.Sscanf(cs.LastLevels["n"], "%d", &cnt)
				cs.LastLevels["n"] = fmt.Sprintf("%d", cnt+1)
				return cs, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	final := LoadCardState(p)
	got := 0
	fmt.Sscanf(final.LastLevels["n"], "%d", &got)
	if got != n {
		t.Errorf("동시 갱신 %d건 중 %d건만 반영됨(유실 발생)", n, got)
	}
}

// fn이 에러를 돌려주면 저장하지 않는다(Upsert 실패 시 CardState를 오염된
// 값으로 덮어쓰면 안 됨).
func TestWithCardStateLockDoesNotSaveOnError(t *testing.T) {
	p := CardStatePath(t.TempDir())
	SaveCardState(p, &CardState{MessageID: "orig"})
	_, err := WithCardStateLock(p, func(cs *CardState) (*CardState, error) {
		cs.MessageID = "changed"
		return nil, fmt.Errorf("upsert 실패")
	})
	if err == nil {
		t.Fatal("에러가 전파돼야 함")
	}
	if s := LoadCardState(p); s.MessageID != "orig" {
		t.Errorf("에러 시 저장되면 안 됨: %+v", s)
	}
}
