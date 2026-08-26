# ConPTY 백엔드 계약 probe

이 독립 모듈은 후보 ConPTY 백엔드에 네이티브 Windows 실행, 입출력, resize, 정상 종료 계약 하나를
적용합니다. 진단용 테스트 코드이며 제품 의존이 아닙니다.

Windows 에서 `go test -count=1 -v ./...` 로 실행합니다.
