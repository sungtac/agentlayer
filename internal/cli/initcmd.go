package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type hookEvent struct{ settingsKey, eventArg string }

// hook 이벤트 ↔ Claude Code settings.json의 이벤트 키.
var claudeEvents = []hookEvent{
	{"PostToolUse", "post-tool-use"},
	{"Notification", "notification"},
	{"Stop", "stop"},
	{"SessionStart", "session-start"},
	{"UserPromptSubmit", "user-prompt-submit"},
}

// hook 이벤트 ↔ stock Gemini CLI settings.json의 이벤트 키.
// (Antigravity CLI의 hooks.json은 형식이 달라 geminiinit.go가 따로 담당.)
var geminiCLIEvents = []hookEvent{
	{"SessionStart", "session-start"},
	{"BeforeAgent", "before-agent"},
	{"AfterTool", "after-tool"},
	{"AfterAgent", "after-agent"},
	{"Notification", "notification"},
}

// InstallClaudeHooks는 Claude Code settings.json에 agentlayer hook을 등록한다.
func InstallClaudeHooks(w io.Writer, settingsPath, binPath string, dryRun bool) error {
	return installJSONHooks(w, settingsPath, binPath, "claude", claudeEvents, dryRun)
}

// InstallGeminiCLIHooks는 stock Gemini CLI의 ~/.gemini/settings.json에
// agentlayer hook을 등록한다. 이벤트 스키마가 Claude Code와 같은 구조
// (이벤트 키 → [{matcher?, hooks: [...]}])라 설치기를 공유한다.
func InstallGeminiCLIHooks(w io.Writer, settingsPath, binPath string, dryRun bool) error {
	return installJSONHooks(w, settingsPath, binPath, "gemini", geminiCLIEvents, dryRun)
}

// installJSONHooks는 settings.json류 파일에 agentlayer hook을 등록한다.
// 원칙: 기존 설정(다른 hook 포함)을 절대 훼손하지 않는다 — map[string]any로
// 읽어 모르는 필드를 그대로 보존하고, 쓰기 전 .agentlayer.bak 백업을 남긴다.
// 이미 등록된 이벤트는 건너뛴다(멱등).
// binPath는 절대 경로 — LaunchAgent가 띄운 세션은 PATH가 최소라 명령
// 이름만 쓰면 hook이 "command not found"로 조용히 죽는다.
// 이전 버전이 등록한 이름-only 항목은 절대 경로로 마이그레이션한다.
func installJSONHooks(w io.Writer, settingsPath, binPath, agent string, events []hookEvent, dryRun bool) error {
	if binPath == "" {
		binPath = "agentlayer"
	}
	settings := map[string]any{}
	raw, err := os.ReadFile(settingsPath)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &settings); err != nil {
			return fmt.Errorf("%s 파싱 실패 — 수동 확인 필요: %w", settingsPath, err)
		}
	case os.IsNotExist(err):
		// 새로 만든다
	default:
		return err
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	changed := false
	for _, ev := range events {
		cmd := binPath + " hook " + agent + " --event " + ev.eventArg
		// agentlayer가 이 agent+event에 등록하는 명령은 항상 "... hook <agent>
		// --event <eventArg>"로 끝난다(바이너리 경로만 다름) — 이름-only 옛
		// 항목이든, 재설치·이동으로 binPath가 바뀌어 남은 예전 절대경로
		// 항목이든 이 접미사로 식별해 전부 제거하고 현재 cmd로 교체한다.
		// (그대로 두면 binPath가 바뀔 때마다 항목이 중복 누적돼 같은 이벤트에
		// 죽은 경로와 새 경로 hook이 동시에 등록된다.)
		suffix := " hook " + agent + " --event " + ev.eventArg
		entries, _ := hooks[ev.settingsKey].([]any)
		if pruned, removed := removeStaleAgentlayerCommands(entries, suffix, cmd); removed {
			entries = pruned
			changed = true
			fmt.Fprintf(w, "  %s: 이전 경로 항목 정리\n", ev.settingsKey)
		}
		if hasCommand(entries, cmd) {
			fmt.Fprintf(w, "  %s: 이미 등록됨 — 건너뜀\n", ev.settingsKey)
			hooks[ev.settingsKey] = entries
			continue
		}
		entries = append(entries, map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": cmd}},
		})
		hooks[ev.settingsKey] = entries
		changed = true
		fmt.Fprintf(w, "  %s: %s\n", ev.settingsKey, cmd)
	}
	settings["hooks"] = hooks

	if dryRun {
		fmt.Fprintln(w, "(dry-run — 파일을 변경하지 않았습니다)")
		return nil
	}
	if !changed {
		return nil
	}

	if raw != nil {
		if err := os.WriteFile(settingsPath+".agentlayer.bak", raw, 0o600); err != nil {
			return fmt.Errorf("백업 실패 — 설치 중단: %w", err)
		}
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		return err
	}
	tmp := settingsPath + ".agentlayer.tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath)
}

// removeStaleAgentlayerCommands는 suffix로 끝나는(=이 agent+event에 대한
// agentlayer 명령인) 엔트리 중 현재 cmd와 다른 것만 제거한다. 이름-only
// 옛 항목과, binPath가 바뀌어 남은 예전 절대경로 항목을 함께 잡아낸다 —
// current와 정확히 같은 항목(이미 올바르게 등록된 상태)은 건드리지 않는다.
func removeStaleAgentlayerCommands(entries []any, suffix, current string) ([]any, bool) {
	var out []any
	removed := false
	for _, e := range entries {
		m, _ := e.(map[string]any)
		inner, _ := m["hooks"].([]any)
		stale := false
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			c, _ := hm["command"].(string)
			if c != current && strings.HasSuffix(c, suffix) {
				stale = true
			}
		}
		if stale {
			removed = true
			continue
		}
		out = append(out, e)
	}
	return out, removed
}

// hasCommand는 hook 엔트리 배열 어딘가에 command가 이미 있는지 확인한다.
func hasCommand(entries []any, cmd string) bool {
	for _, e := range entries {
		m, _ := e.(map[string]any)
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if hm["command"] == cmd {
				return true
			}
		}
	}
	return false
}

// PrintTmuxBinding은 popup 바인딩 안내를 출력한다. .tmux.conf를 자동
// 수정하지 않는다 — 키 바인딩은 사용자의 영역이다.
// binPath는 반드시 절대 경로 — tmux 서버는 최소 PATH(/usr/bin:/bin...)로
// 뜨는 경우가 많아, 명령 이름만 쓰면 팝업이 즉시 닫힌다(깜빡임).
func PrintTmuxBinding(w io.Writer, conflict bool, binPath string) {
	if conflict {
		fmt.Fprintln(w, "⚠ prefix 'a' 키가 이미 바인딩되어 있습니다. 다른 키를 고르세요.")
		return
	}
	if binPath == "" {
		binPath = "agentlayer"
	}
	fmt.Fprintln(w, "tmux 팝업을 쓰려면 ~/.tmux.conf에 다음 한 줄을 추가하세요 (C-b a):")
	fmt.Fprintf(w, "  bind-key a display-popup -E -w 90%% -h 80%% \"%s\"\n", binPath)
	fmt.Fprintln(w, "적용: tmux source-file ~/.tmux.conf")
	fmt.Fprintln(w, "(절대 경로인 이유: tmux 서버는 PATH가 최소한이라 명령 이름만 쓰면 팝업이 바로 닫힙니다)")
}
