package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const flagComponentsV2 = 32768

// noMentions는 모든 발신 payload에 붙인다 — task·session·branch명 등 외부
// 문자열이 카드·핑 텍스트에 그대로 들어가므로, 그 안에 "@everyone"·"@here"·
// 역할 멘션이 섞여도 실제로 알림이 발사되지 않게 원천 차단한다.
var noMentions = map[string]any{"parse": []string{}}

// CardState는 업서트할 메시지 ID와 마지막 level(악화 핑용).
// agentlayer 전용 파일 — discord_dash의 상태 파일과 별개다.
type CardState struct {
	MessageID  string            `json:"message_id,omitempty"`
	LastLevels map[string]string `json:"last_levels,omitempty"`
}

func CardStatePath(stateDir string) string {
	return filepath.Join(stateDir, "discord-card.json")
}

func LoadCardState(path string) *CardState {
	s := &CardState{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}

func SaveCardState(path string, s *CardState) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WithCardStateLock은 path의 CardState를 배타적 파일 잠금(flock) 안에서
// 읽고 fn에 넘긴 뒤, fn이 성공하면 그 결과를 저장한다. state.Store와 같은
// 이유 — temp→rename은 파일 손상만 막을 뿐, 여러 card 프로세스가 동시에
// MessageID·LastLevels를 read-modify-write하는 경쟁(유실)은 막지 못한다.
func WithCardStateLock(path string, fn func(*CardState) (*CardState, error)) (*CardState, error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	updated, err := fn(LoadCardState(path))
	if err != nil {
		return nil, err
	}
	if err := SaveCardState(path, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// Client는 웹훅 호출기. BaseURL은 웹훅 URL 전체.
type Client struct {
	Webhook string
	HTTP    *http.Client
}

func NewClient(webhook string) *Client {
	return &Client{Webhook: webhook, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// 에러 메시지에 웹훅 URL(토큰 포함)을 절대 싣지 않는다.
func (c *Client) request(method, url string, payload any) (map[string]any, int, error) {
	var body *bytes.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, 0, fmt.Errorf("discord 요청 생성 실패")
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "agentlayer")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("discord 요청 실패")
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 카드 응답은 수 KB — 넉넉히
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 400 {
		// 진단용: Discord 에러 본문(웹훅 URL 미포함)을 함께 남긴다
		out = map[string]any{"_error_body": string(raw)}
	}
	return out, resp.StatusCode, nil
}

// Upsert는 카드 메시지 하나를 편집하고, 없으면 새로 만든다.
// 반환값은 최종 message ID.
// 원칙: 옛 메시지 삭제는 새 게시가 성공한 뒤에만 — 카드가 채널에서
// 사라진 채 끝나는 일이 없어야 한다.
func (c *Client) Upsert(components []any, mid string) (string, error) {
	patchFailed400 := false
	if mid != "" {
		res, code, err := c.request(http.MethodPatch,
			fmt.Sprintf("%s/messages/%s?with_components=true", c.Webhook, mid),
			map[string]any{"components": components, "allowed_mentions": noMentions})
		if err != nil {
			return "", err
		}
		if code >= 200 && code < 300 {
			return mid, nil
		}
		switch code {
		case 400:
			patchFailed400 = true // 새 게시 성공 후에만 옛것 삭제
		case 404:
			// 메시지 삭제됨 — 새로 게시
		default:
			return "", fmt.Errorf("discord 편집 실패 (HTTP %d): %v", code, res["_error_body"])
		}
	}
	res, code, err := c.request(http.MethodPost,
		c.Webhook+"?wait=true&with_components=true",
		map[string]any{"username": "agentlayer", "flags": flagComponentsV2, "components": components,
			"allowed_mentions": noMentions})
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("discord 게시 실패 (HTTP %d): %v", code, res["_error_body"])
	}
	id, _ := res["id"].(string)
	if patchFailed400 && mid != "" && id != "" {
		c.request(http.MethodDelete, fmt.Sprintf("%s/messages/%s", c.Webhook, mid), nil)
	}
	return id, nil
}

// Ping은 악화 알림을 일반 메시지로 보낸다 (푸시 발생).
func (c *Client) Ping(content string) error {
	_, code, err := c.request(http.MethodPost, c.Webhook,
		map[string]any{"username": "agentlayer", "content": content, "allowed_mentions": noMentions})
	if err != nil {
		return err
	}
	if code < 200 || code >= 300 {
		return fmt.Errorf("discord 핑 실패 (HTTP %d)", code)
	}
	return nil
}
