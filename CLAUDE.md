<!-- store:session-handoff:start -->
## 세션 이어가기

사용자는 짧게 말하고, 절차는 이 규율이 진다. 재시작 = "이어서 해줘" 한마디, 마감 = "세션 마감" 한마디면 충분하다. 사용자에게 긴 프롬프트나 요약 복붙을 요구하지 말 것.

### 세션 시작 (재정박)
- 폴더에 `SESSION.md`가 있으면 **어떤 작업보다 먼저 읽는다**. 읽기 전 행동 금지.
- 읽은 뒤 첫 응답에서 "현재 상태 + 다음 단계 첫 항목"을 한두 문장으로 복창하고 이어간다.
- `SESSION.md`가 없으면 `SESSION.template.md`를 복사해 만든다(첫 마감 때 채워도 됨).
- 기록에 적힌 파일 경로는 실존 확인 후 사용한다. 없으면 재탐색하고 기록을 정정한다.

### 세션 마감 (체크포인트)
- "세션 마감", "오늘 여기까지" 류의 신호를 받으면 `SESSION.md`를 갱신한다.
- 섹션별 갱신 규칙: **목표**=거의 고정(바뀔 때만 명시 수정) / **현재 상태·다음 단계**=덮어쓰기(짧게) / **결정 기록·파일 흔적**=아래에 추가만(삭제 금지).
- 파일 경로·함수명·에러 메시지는 **그대로 적는다**. 산문에 녹이면 다음 세션이 재탐색하게 된다.
- 갱신 후 자체 점검: "다음 세션이 이 파일만 읽고 '다음 할 일'과 '건드린 파일'을 말할 수 있나?" 못 하면 보강하고 마친다.

### 하지 말 것
- 파일 전체 재작성 — 섹션 규칙대로 증분 갱신만. 재작성할 때마다 세부가 조금씩 소실된다.
- 현재 상태를 길게 쓰기 — 스냅샷은 짧게, 상세는 결정 기록·파일 흔적에.
- 낡은 '현재 상태' 방치 — 마감 신호 없이 세션이 끊겼다면 다음 세션 시작 때 실물과 대조해 정정.
- 결정 기록 삭제 — 뒤집힌 결정도 지우지 말고 "YYYY-MM-DD 그 결정 뒤집음(사유)"로 추가.

### 조건부
- [멀티에이전트도 설치된 경우] `tasks/<작업>/` 안의 작업은 멀티에이전트의 재진입 프로토콜(context.md·log.md)이 정본이다. 그 작업 상태를 SESSION.md에 중복 기록하지 않는다. SESSION.md에는 "지금 어느 task 진행 중" 한 줄 포인터만 둔다.
<!-- store:session-handoff:end -->

## 코드베이스 불변식 (2026-09 코드 리뷰 대응 이후)

관제탑 리뷰(codex-critic × gemini 교차검토 + claude-main 최종검토, 13건 수정)로
확정된 것들 — 이 코드를 다시 만지는 세션은 아래를 되돌리지 않는다.

- **task 이름**: `wt.ValidTaskName`이 `.`·`..`·빈 세그먼트를 거부한다
  (`internal/wt/lifecycle.go`). `wt new`뿐 아니라 `cli.oneTask`·
  `cli.parseTaskAndFlags` 진입점 전부에 적용돼 있다 — 새 wt 서브커맨드를
  추가할 때도 이 검증을 거치게 할 것.
- **task 이름 → 파일명**: `internal/wt/meta.go`의 `sanitizeTaskFilename`이
  유일한 정본이다. `metaPath`·`ReviewPath` 둘 다 이 함수를 쓴다 — 상태
  디렉터리 안에 task명으로 파일 경로를 새로 만들 땐 반드시 이 함수를 거칠 것
  (직접 문자열 결합 금지, "feature/login" 같은 계층형 이름이 다시 ENOENT로
  깨진다).
- **`wt.Merge`**: base가 아닌 브랜치에 있으면 임시 worktree에서 병합한다
  (`internal/wt/git.go`) — 메인 저장소를 무조건 `checkout`하는 방식으로
  되돌리지 말 것. base에 이미 있을 때만 그 자리에서 병합한다.
- **`wt.Clean`**: `Dirty`/`Unmerged` 조회 자체가 실패하면 fail-closed(정리
  거부)다 — `err == nil`일 때만 결과를 반영하는 낙관적 패턴으로 되돌리지
  말 것.
- **resume 명령**: `cli.validSessionID`가 세션 ID를 `^[A-Za-z0-9._-]+$`로
  검증한 뒤에만 셸 명령 문자열에 넣는다. `main.go`의 `runResume`은
  `tmuxx.NewWindowHere`+`SendText` 패턴(RunRestore와 동일)을 쓴다 — 명령을
  `tmux new-window`의 인자로 직접 넘기지 말 것.
- **Discord 발신 payload**: `Upsert`(PATCH/POST)·`Ping`·`notify.go`의 POST
  전부 `allowed_mentions: {parse: []}`를 담는다(`internal/discord/webhook.go`
  의 `noMentions`) — 새 Discord 호출을 추가할 때도 반드시 포함할 것. 카드
  표시 필드(session·task·branch·provider 문구)는 `card.mdEscape`를 거친다.
- **CardState**: `discord.WithCardStateLock`(flock)을 거치지 않고
  `LoadCardState`/`SaveCardState`를 직접 read-modify-write 하지 말 것 —
  `main.go`의 `publishCard`가 유일한 정본 호출부다.
- **`RunCoalesced`**: publish 실패 시 `card.dirty` 마커를 복구한다
  (`internal/discord/coalesce.go`) — 성공 여부와 무관하게 먼저 지우는
  방식으로 되돌리면 재시도 신호가 영구 소실된다.
- **`WorsenedPings`**: 최초 관찰(`last` 맵에 키가 없음)이어도 이미
  red/yellow면 핑을 보낸다(`internal/discord/card.go`) — `seen`일 때만
  비교하는 방식으로 되돌리지 말 것.
- **알림 설정 키**: 정식 키는 `notify_desktop`(`config.NotifyDesktop`,
  `DesktopNotifyEnabled()`)이다. `notify_macos`/`MacOSEnabled()`는
  하위호환 별칭으로만 유지 — 새 코드에서 참조하지 말 것.
- **`usage.AgyCtx`**: `transcript.jsonl`을 우선하고, 없을 때만
  `transcript_full.jsonl`로 근사 폴백한다 — "더 큰 파일" 기준으로
  되돌리면 ctx%가 다시 상시 100%로 포화 표시된다.
- **Linux/WSL2**: `wiring.scanUnitDir`가 LaunchAgent(macOS plist)와
  systemd `--user` 서비스(`Paths.SystemdUserDir`, `.service`) 둘 다
  스캔한다. `usage.toolDirs`에 Linuxbrew(`/home/linuxbrew/.linuxbrew/bin`)·
  Snap(`/snap/bin`)이 포함돼 있다 — 이 두 곳을 다시 macOS 전용으로
  좁히지 말 것.

각 항목의 회귀 테스트는 `git log`에서 커밋 `fix: P0 3건...`·`fix: P1 4건...`·
`fix: Linux/WSL2 체감 항목 6건...` 3개를 참고(각 커밋 메시지에 근거·시나리오
상세).
