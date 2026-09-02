package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codexConfigFixture = `model = "gpt-5.6"
approval_policy = "on-request"

[mcp_servers.context7]
command = "npx"
`

func TestInstallCodexNotifyInsertsBeforeSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(codexConfigFixture), 0o600)
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	content := string(b)
	notifyIdx := strings.Index(content, "notify =")
	sectionIdx := strings.Index(content, "[mcp_servers")
	if notifyIdx < 0 || sectionIdx < 0 || notifyIdx > sectionIdx {
		t.Errorf("notify는 섹션 앞 최상위에:\n%s", content)
	}
	if !strings.Contains(content, `"/abs/agentlayer", "hook", "codex"`) {
		t.Errorf("명령 배열:\n%s", content)
	}
	if !strings.Contains(content, `model = "gpt-5.6"`) {
		t.Error("기존 설정 보존")
	}
	if _, err := os.Stat(p + ".agentlayer.bak"); err != nil {
		t.Error("백업 생성")
	}
}

// 회귀 테스트: 첫 섹션 헤더 "바로 앞줄"에 빈 줄 없이 키가 있는 경우(가장 흔한
// config.toml 형태). 위 TestInstallCodexNotifyInsertsBeforeSection의 픽스처는
// 헤더 앞에 빈 줄이 있어 예전 idx 계산 버그(개행 하나 덜 셈)가 우연히 가려졌다 —
// 빈 줄이 없으면 새 notify 줄이 직전 줄과 개행 없이 붙어(`"gpt-5.6"notify = [...]`)
// TOML 자체가 깨졌었다.
func TestInstallCodexNotifyInsertsBeforeSectionNoBlankLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("model = \"gpt-5.6\"\n[mcp_servers.context7]\ncommand = \"npx\"\n"), 0o600)
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	content := string(b)
	if strings.Contains(content, "gpt-5.6\"notify") {
		t.Fatalf("이전 줄과 개행 없이 붙음(TOML 파손):\n%s", content)
	}
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || lines[0] != `model = "gpt-5.6"` ||
		!strings.HasPrefix(lines[1], "notify =") || lines[2] != "[mcp_servers.context7]" {
		t.Errorf("줄 구조가 어긋남:\n%s", content)
	}
}

func TestInstallCodexNotifyExistingSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	orig := "notify = [\"my-notifier\"]\n" + codexConfigFixture
	os.WriteFile(p, []byte(orig), 0o600)
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != orig {
		t.Error("기존 notify가 있으면 무변경 (사용자 설정 우선)")
	}
	if !strings.Contains(buf.String(), "건너뜀") {
		t.Error("건너뜀 안내")
	}
}

func TestInstallCodexNotifyDryRun(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(codexConfigFixture), 0o600)
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != codexConfigFixture {
		t.Error("dry-run 무변경")
	}
}

func TestInstallCodexNotifyNoFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "notify =") {
		t.Error("파일 없으면 새로 생성")
	}
}

func TestInstallCodexNotifyNoSections(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("model = \"x\"\n"), 0o600)
	var buf bytes.Buffer
	if err := InstallCodexNotify(&buf, p, "/abs/agentlayer", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "notify =") || !strings.Contains(string(b), "model") {
		t.Errorf("섹션 없는 파일 끝에 추가:\n%s", b)
	}
}
