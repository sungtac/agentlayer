# agentlayer — iTerm2+tmux 멀티 에이전트 관제탑

tmux 안에서 돌아가는 Claude Code / Codex / Gemini 에이전트들이
**누가 일하는 중이고, 누가 입력을 기다리고, 누가 끝났는데 아직 안 봤는지**를
한 화면에서 보여주는 터미널 도구.

Orca ADE의 관제 기능을 일반 tmux 위에 재현한다 — 자체 터미널도, GUI도,
데몬도 없다. tmux가 이미 잘하는 것(세션 유지, SSH 재접속)은 tmux에 맡기고,
tmux가 모르는 것(에이전트의 의미 상태)만 채운다.

[coach](https://github.com/netwaif/usage-coach)(사용량 코칭) ·
[mat](https://github.com/netwaif/mat)(MultiAgent 작업 관제)의 자매 도구.

## 상태 모델

```
● WORK   일하는 중 (hook heartbeat)
◆ WAIT   사용자 입력·승인 대기  ← 가장 위에 정렬
✔ DONE   끝났는데 아직 안 봄 (읽음 처리 전까지 유지)
✖ ERR    비정상 종료
· idle   대기
  dead   pane 소실 (24시간 뒤 자동 정리)
```

상태는 **화면 스크래핑 없이** 에이전트 공식 hook과 tmux 메타데이터로만
판정한다. `DONE → idle` 전환은 반드시 사용자 행동(점프·읽음 키)으로만
일어난다 — "끝났는데 안 본 것"을 놓치지 않는 게 이 도구의 존재 이유다.

## 설치

```bash
brew install netwaif/tap/agentlayer
# 또는 소스 빌드 (Go 1.22+)
git clone https://github.com/netwaif/agentlayer.git && cd agentlayer
make install   # ~/.local/bin/agentlayer
```

설정은 한 번:

```bash
agentlayer init            # Claude hook 등록 (기존 hook 보존, 백업 생성)
agentlayer init --dry-run  # 뭘 바꾸는지 먼저 확인
```

tmux 팝업(`C-b a`)을 쓰려면 init이 안내하는 한 줄을 `.tmux.conf`에 추가한다.
agentlayer는 tmux 설정을 자동으로 수정하지 않는다.

**Linux/WSL2**: macOS 전용이었던 것을 이식했다 — 알림은 `notify-send`,
구동 주체 탐지는 systemd `--user` 서비스(`~/.config/systemd/user/*.service`,
LaunchAgent와 동일한 방식), 외부 도구(coach 등) 탐색 경로에 Linuxbrew
(`/home/linuxbrew/.linuxbrew/bin`)·Snap(`/snap/bin`)도 포함한다.

## 사용

```bash
agentlayer            # TUI 관제탑 (j/k 이동, enter 점프+읽음, o 읽음, u 사용량 뷰, r 새로고침, q 종료)
agentlayer status     # plain 표 — SSH·스크립트용
agentlayer status --json
agentlayer card       # Discord 상태 카드 업서트 (주기 실행용) / --out은 JSON만
agentlayer resume     # 죽은 claude 대화 목록 / resume <id>로 구조
agentlayer wake-all   # 모든 claude·codex 세션에 "세션 이어서하자" 일괄 전송
agentlayer close-all  # "세션 마감하자" 전송 → 전원 완료(DONE)까지 감시 → 요약
agentlayer broadcast "<메시지>"   # 임의 메시지 일괄 전송 (--except로 제외, --yes로 무확인)
agentlayer info <세션>            # 배선 상세 카드: 폴더·엔진·Discord 채널·구동 주체·resume 경로
agentlayer wt ...     # worktree 병렬 모드 (아래 참고)
```

## 에이전트별 상태 신호

| 에이전트 | 신호 | 상태 |
|---|---|---|
| Claude Code | hooks (PostToolUse/Notification/Stop) | WORK/WAIT/DONE 전부 |
| Codex | config.toml notify (turn-complete) | DONE (나머지는 스캐너) |
| Gemini | 프로세스·pane 감지 | 존재·소실만 (lifecycle 신호 없음) |

`agentlayer init` 한 번으로 Claude hook과 Codex notify가 함께 등록된다
(기존 설정 보존·백업·멱등).

## 사용량·컨텍스트 (선택적 통합)

[usage-coach](https://github.com/netwaif/usage-coach)가 설치돼 있으면:

- TUI 헤더에 provider별 요약 한 줄, `u` 키로 전용 뷰(게이지·리셋·코칭)
- 각 에이전트 행에 모델·컨텍스트%·마지막 활동 (statusline 스냅샷 + codex rollout)
- coach 콜드 실행이 느려도 5분 파일 캐시 + 중복 실행 방지로 TUI는 블로킹되지 않는다

## 알림·Discord

`~/.config/agentlayer/config.json`:

```json
{
  "discord_webhook_url": "https://discord.com/api/webhooks/...",
  "notify_webhook_url": "https://discord.com/api/webhooks/...",
  "notify_desktop": true,
  "notify_discord": false
}
```

- 에이전트가 **완료(DONE)** 되거나 **입력 대기(WAIT)** 로 바뀐 순간에만 알림 1회
  (heartbeat는 무음) — 데스크톱 알림(macOS `osascript` / Linux `notify-send`) +
  (켜면) Discord 단문
- `notify_desktop`(선택, 기본 켜짐): 데스크톱 알림 스위치. 예전 이름
  `notify_macos`도 하위호환으로 계속 인식한다(둘 다 있으면 `notify_desktop` 우선)
- Discord로 나가는 모든 메시지(카드·핑)는 `allowed_mentions`로 멘션을 전부
  차단한다 — task명·브랜치명에 `@everyone` 등이 섞여도 실제로 발사되지 않는다
- `notify_webhook_url`(선택): 단문 알림을 별도 알림 채널로 분리. 비우면 카드
  웹훅으로 감. 분리하면 대시보드 채널이 카드 한 장짜리로 유지돼 스크롤이 없다
- `agentlayer card`는 사용량 + 에이전트 상태를 Discord 메시지 하나로 계속
  업서트하고, provider level이 악화되면 새 메시지로 핑한다.
  LaunchAgent 등으로 5분 주기 실행을 권장
- `preview_interval`(선택): TUI 미리보기 갱신 주기. Go duration 문자열
  (`"500ms"`, `"2s"`). 기본 `1s`, 하한 200ms(그 아래는 200ms로 보정).
  목록 폴링(2초)과는 별개로 미리보기만 조절된다. TUI 재시작 시 적용

## Worktree 병렬 모드

같은 저장소에서 여러 에이전트(claude/codex/gemini 혼합 자유)가 서로 파일을
밟지 않고 병렬 작업하게 한다. 명령 하나가 worktree + `agent/<task>` 브랜치 +
tmux window + 에이전트 실행까지 만든다.

```bash
agentlayer wt new auth-api --agent claude --test 'go test ./...'
agentlayer wt new login-ui --agent codex        # 같은 repo, 충돌 없음
agentlayer wt list                              # dirty·미병합·테스트 상태 한눈에
agentlayer wt diff auth-api                     # base 대비 변경
agentlayer wt test auth-api                     # 테스트 실행·기록
agentlayer wt review auth-api                   # 리뷰 파일 생성 → "#> 코멘트" 작성
agentlayer wt send auth-api                     # 코멘트를 에이전트에 수정 지시로 전송
agentlayer wt merge auth-api                    # 검사 요약 + 명령 안내 + y 확인 후 병합
agentlayer wt clean auth-api                    # 보존 우선 정리
```

- **자동 merge 없음** — merge는 항상 안내 + 명시적 확인
- **보존 우선 정리** — 미커밋·untracked·미병합 커밋이 하나라도 있으면 clean 거부.
  이 확인 자체가 실패해도(권한 오류 등) "깨끗함"으로 넘겨짚지 않고 거부한다
- **merge가 현재 브랜치를 안 건드림** — 메인 저장소가 base가 아닌 다른
  브랜치에 있으면 임시 worktree에서 병합해, 지금 보고 있는 브랜치·미커밋
  작업을 전혀 침범하지 않는다
- worktree는 `<repo>/.agentlayer/worktrees/<task>`에, 메타는 상태 디렉터리에 기록
- task 이름은 `..`·빈 세그먼트를 거부한다(계층형 이름 `feature/login`은 허용) —
  상태 디렉터리 밖으로 경로가 새는 것을 막는다

## 안전 원칙

- 기존 tmux 세션·window·pane을 절대 kill하지 않는다
- 기존 prefix·키 바인딩을 변경하지 않는다 (`C-b a`는 옵트인)
- settings.json·config.toml 수정 전 백업(`*.agentlayer.bak`)을 만든다
- 상태 파일(`~/.local/state/agentlayer/`)에 인증정보를 쓰지 않는다
- Mac이 sleep/재부팅하면 tmux와 에이전트 프로세스는 유지되지 않는다
  (tmux-resurrect 등과 병용 권장)
- worktree task 이름은 경로 탈출 문자(`..`)를 거부하고, `wt merge`는 현재
  체크아웃을 건드리지 않으며, `wt clean`은 안전 확인이 실패하면 거부한다
- Discord로 나가는 모든 메시지는 `allowed_mentions`로 멘션을 차단한다

## 로드맵

- **Phase 1 ✔**: 상태 관제 코어 — TUI·status CLI·Claude hook
- **Phase 2 ✔**: 사용량 뷰(coach 통합)·macOS/Discord 알림·Discord 상태 카드
- **Phase 3 ✔**: worktree 병렬 모드 — 생성·diff 코멘트 회신·테스트 수집·보존 우선 정리
- **Phase 4 ✔**: Codex 어댑터·MultiAgent 패널·비상 resume·배포 준비
- **v1.0 ✔**: Gemini(Antigravity CLI·stock CLI) 완전 편입 — hook 상태추적·모델·ctx(근사)·resume,
  3사 그룹 정렬·구분선, 기본모델 헤더(Fable 경고), GitHub 공개 + brew tap
- **2026-09 안전성 패치 ✔**: 코드 리뷰(codex-critic × gemini 교차검토 +
  claude-main 최종검토) 대응 — Codex config.toml 삽입 파손, worktree 경로
  탈출, `wt clean` fail-open, `wt merge`의 현재 브랜치 침범, resume 세션 ID
  셸 인젝션, Discord 멘션·마크다운 이스케이프, 위험 알림 3중 침묵(CardState
  경쟁·최초관찰 누락·재시도 유실) 수정. Linux/WSL2 이식 보강 — systemd
  `--user` 서비스 탐지, Linuxbrew/Snap 경로, `notify_desktop` 키 정리,
  Antigravity ctx% 과대추정, MultiAgent 헤더 파싱, 계층형 task명 리뷰 파일
