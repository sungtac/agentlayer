// Package notify는 상태 전이 알림을 보낸다. 발화 조건은 호출자가 판단하고
// (실제 상태 변경 시에만), 이 패키지는 전달만 한다. 실패는 조용히 무시 —
// 알림 때문에 hook이 에이전트를 방해하면 안 된다.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/netwaif/agentlayer/internal/config"
	"github.com/netwaif/agentlayer/internal/state"
)

// Sender는 전달 수단 주입점 (테스트용).
type Sender struct {
	RunOSA   func(title, body string) error      // 데스크톱 알림(mac=osascript, linux=notify-send)
	PostJSON func(url string, body []byte) error // Discord 웹훅 POST
}

// DefaultSender는 실제 전달 수단. RunOSA는 GOOS별로 갈라진다 — 필드명은
// macOS 시절 그대로 유지(호출부·테스트 호환), 구현만 플랫폼에 맞춘다.
func DefaultSender() Sender {
	return Sender{
		RunOSA: runDesktopNotify,
		PostJSON: func(url string, body []byte) error {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(url, "application/json", bytes.NewReader(body))
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		},
	}
}

// runDesktopNotify는 title·body를 플랫폼 알림으로 낸다.
// macOS는 osascript(display notification), Linux는 notify-send가 PATH에
// 있을 때만(WSL2는 데스크톱 알림 데몬이 기본 없음) 시도하고, 없으면 조용히
// 무시 — 알림 실패로 hook을 막지 않는다는 원칙은 그대로 유지.
//
// 실행은 Run()이 아니라 Start()+Release()로 띄우고 기다리지 않는다: hook은
// Claude/Codex/Gemini CLI가 응답을 기다리는 동기 프로세스라, 알림 데몬이
// 멈추거나 응답이 늦으면(D-Bus 타임아웃 등) Run()은 그 시간만큼 hook 전체를
// 막는다 — "실패는 무시해도 hang은 안 막아준다"는 게 핵심 결함이었다.
// main.go의 카드 갱신 트리거와 같은 detached 패턴.
func runDesktopNotify(title, body string) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		script := fmt.Sprintf("display notification %q with title %q", body, title)
		cmd = exec.Command("osascript", "-e", script)
	default:
		if _, err := exec.LookPath("notify-send"); err != nil {
			return nil // 알림 데몬 없음 — Discord 알림으로 대체되는 걸 전제
		}
		cmd = exec.Command("notify-send", title, body)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// notifiable은 알림 가치가 있는 전이 목적지.
func notifiable(to state.AgentState) bool {
	return to == state.StateDoneUnread || to == state.StateWaiting || to == state.StateError
}

func title(a *state.Agent, to state.AgentState) string {
	name := a.Tmux.Session
	if name == "" {
		name = a.Kind
	}
	switch to {
	case state.StateDoneUnread:
		return fmt.Sprintf("✔ %s 완료", name)
	case state.StateWaiting:
		return fmt.Sprintf("◆ %s 입력 대기", name)
	default:
		return fmt.Sprintf("✖ %s 에러", name)
	}
}

// Notify는 prev→to 전이를 알린다. 같은 상태(heartbeat)나 알림 가치가 없는
// 전이는 무음. 웹훅 URL은 어떤 에러 경로에서도 출력하지 않는다.
func Notify(cfg *config.Config, s Sender, a *state.Agent, prev, to state.AgentState) {
	if prev == to || !notifiable(to) {
		return
	}
	t := title(a, to)
	body := a.Task
	if body == "" {
		body = a.CWD
	}
	if cfg.DesktopNotifyEnabled() && s.RunOSA != nil {
		_ = s.RunOSA(t, body)
	}
	url := cfg.NotifyURL()
	if cfg.NotifyDiscord && url != "" && s.PostJSON != nil {
		payload, err := json.Marshal(map[string]any{
			"username":         "agentlayer",
			"content":          fmt.Sprintf("%s — %s", t, body),
			"allowed_mentions": map[string]any{"parse": []string{}},
		})
		if err == nil {
			_ = s.PostJSON(url, payload)
		}
	}
}
