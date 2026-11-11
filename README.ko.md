[English](README.md)

# why-hwp

한글 문서(HWP / HWPX)를 Windows, Linux, macOS 에서 다루는 명령줄 도구입니다. 실행 파일 하나로 돌아가며 Python, Java 같은 런타임이 필요 없습니다.

> **상태: 개발 초기.** 아직 쓸 수 있는 기능이 없습니다. 이 문서는 완성된 기능이 아니라 목표를 적은 것입니다.

## 왜 만드나

스크립트로 HWP 문서를 고치려면 보통 둘 중 하나가 필요합니다.

- Windows 에 한컴오피스를 깔고 COM 자동화로 호출
- Python 이나 Java 라이브러리를 쓰기 위해 런타임부터 설치

why-hwp 는 Go 로 작성해 플랫폼마다 실행 파일 하나로 빌드합니다. 파일을 복사할 수 있는 곳이면 같은 도구가 그대로 돌아갑니다.

## 목표

- **HWPX** 문서를 직접 읽고 고칩니다 (HWPX 는 XML 파일을 묶은 zip 이라 모든 플랫폼에서 됩니다)
- 원래 서식을 유지합니다. 문서에 이미 있는 문단·글자 모양을 그대로 씁니다
- 고친 내용을 **변경 추적**으로 남겨, 한글의 검토 화면에서 보이게 합니다
- 그림은 들어갈 영역에 맞는 가장 높은 해상도로 넣습니다
- 기존 메모와 다른 사람이 쓴 내용은 건드리지 않습니다
- 한컴오피스가 설치된 Windows 에서는 HWP 와 HWPX 사이를 변환합니다

## 지원 플랫폼

| OS | 아키텍처 |
|----|----------|
| Windows | amd64, arm64 |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

## 명령

### svg2png

```sh
why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-o out.png] <in.svg>
```

SVG 를 문서에 들어갈 너비에 맞춘 PNG 로 만듭니다(기본 600 dpi, 높이는 SVG 비율대로). headless Chrome·Edge·Chromium·Firefox 로 렌더링하므로, SVG 에 data URI 로 넣은 웹폰트를 포함해 글꼴이 브라우저에서 보이는 그대로 나옵니다. 브라우저는 `-browser` 나 `WHY_HWP_BROWSER` 로 고르고, `-trim` 은 흰 여백을 잘라냅니다. 너비를 주지 않으면 SVG 고유 크기(1인치 = 96px)를 씁니다. 출력은 한 변 20000px, 전체 1억 2천만 픽셀까지입니다.

## 빌드

Go 1.24 이상과 GNU make 가 필요합니다. Windows 에서는 Git Bash 에서 make 를 실행합니다.

```sh
make            # 현재 OS 용으로 bin/ 에 빌드
make dist       # 6개 플랫폼을 dist/why-hwp-<os>-<arch>[.exe] 로 빌드
make checksums  # dist/ 를 빌드하고 dist/SHA256SUMS 생성
make test       # go test ./...
```

`why-hwp version` 이 출력하는 버전은 `git describe --tags --always --dirty` 에서 옵니다. `make VERSION=v1.0.0` 으로 바꿀 수 있습니다.

## 라이선스

[Apache License 2.0](LICENSE)
