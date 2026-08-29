# soksak-sidecar-pty

셸을 소유하고 바이트를 이동하며 바이트의 의미는 읽지 않습니다.
`soksak-spec-sidecar-pty`를 구현합니다.

## 프로세스로 분리하는 이유

이 사이드카가 시작한 셸은 앱이 재시작하거나 창이 닫혀도 유지됩니다. 출력 링은
렌더러가 없는 동안의 출력을 보관하고 절대 출력 순서와 흐름 제어 기준을 유지합니다.
상태는 각 PTY에 마지막으로 성공적으로 적용된 크기와 원본 이벤트 순서를 반환합니다.

`process.inventory`는 프로세스 모니터가 읽는 소유자 snapshot입니다. 이 사이드카가 시작한 셸을
owner, window, pane, pid, command, state, timestamp와 함께 명시적으로 반환합니다. 자식 프로세스
`process.observe`는 초기 snapshot 뒤 하나의 연결을 유지하며 peer가 닫을 때까지 revision이 붙은
`started`/`ended` record를 보냅니다. 자식 프로세스 전체와 updated event는 별도 gate이므로 이것만으로
완성된 모니터라고 주장하지 않습니다.

세션의 렌더러는 하나이며, 마지막에 붙은 쪽입니다. 떼지 않고 사라진 실행이 표식을 남겼다고 해서 다음
부착을 거부하면, 그 pane 은 다시는 아무것도 그릴 수 없습니다. 사라진 쪽은 더 낮은 세대를 들고 있으므로
그쪽의 detach 가 자리를 대신한 쪽을 떼어내지 못합니다.

렌더러가 없고 출력도 30분 동안 없는 세션은 사라진 실행이 남긴 것이며 종료됩니다 — 아무도 그 셸에 닿을
수 없는데 프로세스와 출력 링과 파일 서술자를 붙들고 있습니다. 출력이 계속 나오는 세션은 누군가를 위해
일하는 중이므로, 무엇이 붙어 있든 여기서 끝내지 않습니다.

## 소유하지 않는 것

escape sequence, 화면, scrollback grid, prompt를 해석하지 않습니다. 판 ID와 창 label은
요청과 상태 사이를 그대로 이동하며 이 프로세스는 그 의미를 결정하지 않습니다.

## 통신

모든 연결은 하나의 control address에서 시작하고 첫 요청의 token을 확인합니다.
`pty.attach`는 성공 응답 뒤 같은 연결을 raw output stream으로 전환합니다. 준비 완료는
프로세스가 stdout에 쓰는 첫 JSON 줄로만 판정합니다.

## 제공하지 않는 기능

- 안전한 live handoff는 아직 제공하지 않습니다. status는 level 0을 반환하고 명령을
  명시적으로 거부합니다.
- release는 `release/targets.json`에 선언된 target만 게시합니다. ConPTY는 Windows owner
  tests와 설치된 terminal system suite로 검증하며 cross-build만으로 runtime 성공을
  주장하지 않습니다.

## 빌드 계약

Go 버전 정본은 `go.mod` 하나이고, 공개 target 정본은 `release/targets.json` 하나입니다. Make
공개 진입점에는 native target을 명시해야 합니다. 실제 Go runtime이 target과 다르면 dependency
준비나 컴파일 전에 exit 78로 거부합니다.

```sh
make verify TARGET=aarch64-apple-darwin
make stage TARGET=aarch64-apple-darwin OUT=dist
```

`build`는 `target/<target>/release/soksak-sidecar-pty[.exe]`만 만듭니다. `stage`는 컴파일하지
않고 선언된 산출물만 복사하며, 반복 실행에서 기존 파일과 byte가 다르면 실패합니다. 릴리스
Actions도 `go.mod`에서 toolchain을 설치한 뒤 모든 target에 같은 Make 명령을 실행합니다.
immutable spec validator는 source checkout이나 sibling 경로가 아니라 release train이 전달한
URL과 SHA-256으로만 주입합니다.
