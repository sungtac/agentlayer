package notify

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/netwaif/agentlayer/internal/config"
	"github.com/netwaif/agentlayer/internal/state"
)

type capture struct {
	osa     []string
	posts   []string
	postURL []string
}

func sender(c *capture) Sender {
	return Sender{
		RunOSA: func(title, body string) error { c.osa = append(c.osa, title+" "+body); return nil },
		PostJSON: func(u string, b []byte) error {
			c.postURL = append(c.postURL, u)
			c.posts = append(c.posts, string(b))
			return nil
		},
	}
}

func agent() *state.Agent {
	return &state.Agent{ID: "claude-7", Kind: "claude", Task: "빌드 승인 필요",
		Tmux: state.TmuxRef{Session: "collab-bot", PaneID: "%7"}}
}

func TestNotifyOnDoneAndWaiting(t *testing.T) {
	for _, to := range []state.AgentState{state.StateDoneUnread, state.StateWaiting, state.StateError} {
		c := &capture{}
		Notify(&config.Config{}, sender(c), agent(), state.StateWorking, to)
		if len(c.osa) != 1 {
			t.Errorf("%s: macOS 알림 1회, got %d", to, len(c.osa))
		}
		if !strings.Contains(c.osa[0], "collab-bot") {
			t.Errorf("세션명 포함: %q", c.osa[0])
		}
	}
}

func TestHeartbeatSilent(t *testing.T) {
	c := &capture{}
	Notify(&config.Config{}, sender(c), agent(), state.StateWorking, state.StateWorking)
	Notify(&config.Config{}, sender(c), agent(), state.StateIdle, state.StateWorking) // WORKING 진입도 무음
	if len(c.osa)+len(c.posts) != 0 {
		t.Error("heartbeat·WORKING 진입은 무음")
	}
}

func TestMacOSDisabled(t *testing.T) {
	c := &capture{}
	off := false
	Notify(&config.Config{NotifyMacOS: &off}, sender(c), agent(), state.StateWorking, state.StateDoneUnread)
	if len(c.osa) != 0 {
		t.Error("꺼짐 설정 반영")
	}
}

func TestDiscordOnlyWhenEnabled(t *testing.T) {
	c := &capture{}
	cfg := &config.Config{DiscordWebhookURL: "https://discord.com/api/webhooks/1/x"}
	Notify(cfg, sender(c), agent(), state.StateWorking, state.StateDoneUnread)
	if len(c.posts) != 0 {
		t.Error("notify_discord 꺼져 있으면 웹훅 미전송")
	}
	cfg.NotifyDiscord = true
	Notify(cfg, sender(c), agent(), state.StateWorking, state.StateDoneUnread)
	if len(c.posts) != 1 {
		t.Fatal("켜면 전송")
	}
	if !strings.Contains(c.posts[0], "완료") || !strings.Contains(c.posts[0], "빌드 승인 필요") {
		t.Errorf("내용: %s", c.posts[0])
	}
	if strings.Contains(c.posts[0], "webhooks/1/x") {
		t.Error("본문에 웹훅 URL 노출 금지")
	}
}

// notify_webhook_url이 있으면 단문 알림은 그쪽(알림 채널)으로 간다 —
// 대시보드 채널(discord_webhook_url)은 카드 전용으로 유지.
func TestNotifyUsesDedicatedWebhook(t *testing.T) {
	c := &capture{}
	cfg := &config.Config{NotifyDiscord: true,
		DiscordWebhookURL: "https://card.example",
		NotifyWebhookURL:  "https://alerts.example"}
	Notify(cfg, sender(c), agent(), state.StateWorking, state.StateDoneUnread)
	if len(c.postURL) != 1 || c.postURL[0] != "https://alerts.example" {
		t.Errorf("알림 전용 웹훅으로 가야 함: %v", c.postURL)
	}
}

// TestRunDesktopNotifyDoesNotBlockOnSlowHandler는 agentlayer-review-execute-top5
// 카테고리3 CONFIRMED 회귀 테스트: notify-send를 Run()으로 동기 실행하면 알림
// 데몬이 멈췄을 때 hook 프로세스 전체가 그만큼 멈췄다. 일부러 느린(3초 sleep)
// 가짜 notify-send를 PATH에 심어두고, runDesktopNotify가 그 완료를 기다리지
// 않고 훨씬 빨리 반환하는지 확인한다.
func TestRunDesktopNotifyDoesNotBlockOnSlowHandler(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("이 테스트는 notify-send 경로(비-macOS) 전용")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 3\n"
	fake := filepath.Join(dir, "notify-send")
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	start := time.Now()
	if err := runDesktopNotify("제목", "본문"); err != nil {
		t.Fatalf("Start 단계 자체는 성공해야 함: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("느린 notify-send(3초 sleep)를 기다리며 블록됨: %v 걸림", elapsed)
	}
}

// notify_webhook_url 미설정이면 기존처럼 카드 웹훅으로 폴백 — 하위 호환.
func TestNotifyFallsBackToCardWebhook(t *testing.T) {
	c := &capture{}
	cfg := &config.Config{NotifyDiscord: true, DiscordWebhookURL: "https://card.example"}
	Notify(cfg, sender(c), agent(), state.StateWorking, state.StateDoneUnread)
	if len(c.postURL) != 1 || c.postURL[0] != "https://card.example" {
		t.Errorf("미설정 시 카드 웹훅 폴백: %v", c.postURL)
	}
}
