# soksak-sidecar-pty

셸을 소유하고 바이트를 이동하며 바이트의 의미는 읽지 않습니다.
`soksak-spec-sidecar-pty`를 구현합니다.

## 프로세스로 분리하는 이유

이 사이드카가 시작한 셸은 앱이 재시작하거나 창이 닫혀도 유지됩니다. 출력 링은
렌더러가 없는 동안의 출력을 보관하고 절대 출력 순서와 흐름 제어 기준을 유지합니다.
상태는 각 PTY에 마지막으로 성공적으로 적용된 크기와 원본 이벤트 순서를 반환합니다.

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
