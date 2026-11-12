[English](README.md)

# why-hwp

한글 문서(HWP / HWPX)를 Windows, Linux, macOS 에서 다루는 명령줄 도구입니다. 실행 파일 하나로 돌아가며 Python, Java 같은 런타임이 필요 없습니다.

> **상태: 개발 초기.** 아래 명령은 동작하고 Windows 와 Linux 에서 시험했지만, 1.0 전까지 옵션과 명세 형식이 바뀔 수 있습니다.

## 왜 HWP 인가

이름은 한숨에서 왔습니다. 한글 문서를 스크립트로 만져 본 사람이라면 한 번쯤 해 봤을 그 말, "왜... 하필 HWP 야?" 고를 수는 없습니다. 보고서도 서식도 제출물도 HWP 로 오고, HWP 로 돌려보내야 합니다. 그래서 why-hwp 는 그만 묻고 그냥 다룹니다. 어느 OS 에서든, 스크립트 한 줄로.

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
- 내장 엔진으로 모든 플랫폼에서 한컴오피스 없이 HWP·HWPX 를 HWP, HWPX, PDF, UTF-8 텍스트로 변환합니다 (Windows 에서는 한컴오피스로 변환할 수도 있습니다)

## 지원 플랫폼

| OS | 아키텍처 |
|----|----------|
| Windows | amd64, arm64 |
| Linux | amd64, arm64 |
| macOS | amd64, arm64 |

모든 명령이 모든 플랫폼에서 됩니다. `convert -engine hancom` 만 한컴오피스가 설치된 Windows 가 필요합니다.

## 명령

### text

```sh
why-hwp text [-format plain|json] [-view accepted|original|markup] <파일>
```

`.hwpx`·`.hwp`·`.docx` 의 글을 한 줄에 한 덩어리씩 `<위치><TAB><글>` 로 출력합니다. 위치로 문서 안의 자리를 다시 찾을 수 있습니다. `s0/p12` 는 0번 구역의 12번 문단이고, `s0/p3/t0/r1/c2` 는 3번 문단에 있는 첫 표의 1행 2열입니다. 문단이 여러 개인 칸은 문단마다 한 줄씩 나옵니다(`s0/p3/t0/r1/c2/p0`, `s0/p3/t0/r1/c2/p1`, ...). 문단이 하나인 칸은 칸 위치로 한 줄입니다. 이 위치들이 `apply` 가 받는 위치입니다. 글상자 안의 글은 글상자가 놓인 문단의 글에 넣지 않고, 그 문단 뒤(문단에 표가 있으면 표의 칸들 뒤)에 따로 한 줄로 나옵니다. 위치는 그 문단의 loc 에 `#box` 를 붙인 것이고(`s0/p12#box`), 글상자 문단 사이는 `\n` 입니다. `#box` 줄은 읽기 전용입니다. `apply` 는 글상자 안의 글을 고치지 않습니다. 메모 내용, 머리말·꼬리말, 각주는 넣지 않습니다.

변경 추적이 있는 문서는 `-view` 로 보는 모습을 고릅니다. `accepted`(기본)는 변경을 모두 수락한 글로, 줄바꿈과 공백을 빼면 한글이 TXT 로 저장하는 글과 같습니다. `original` 은 변경을 모두 거절한 글이고, `markup` 은 둘 다 보여 주며 넣은 글을 `[+글+]`, 지운 글을 `[-글-]` 로 표시합니다(문단 나눔이 바뀌었으면 표시 안에 `\n` 으로 나옵니다). 위치는 보기에 따라 바뀌지 않습니다. 지운 문단과 넣은 문단을 포함해 저장된 문단 순서대로 번호를 매기므로 `apply` 가 받는 위치와 같습니다. 다른 보기에만 있는 문단은 줄이 없고, 보기에서 문단 나눔이 없어진 문단은 같은 구역이나 같은 칸의 다음 문단과 합쳐서 보여 줍니다. 보기는 `.hwpx` 와 `.docx`(`w:ins`, `w:del`, `w:moveFrom`, `w:moveTo`)에서 되고, `.hwp` 는 보기를 지원하지 않습니다.

### info

```sh
why-hwp info [-format plain|json] [-view accepted|original|markup] <파일>
```

구역·문단 수, 표와 그 크기, `(A)`·`(A/B)` 같은 담당 표기와 나온 위치, 메모, 변경 추적, 스타일, 그림을 보여줍니다. 담당 표기는 `-view`(기본 `accepted`)의 글에서 셉니다. 변경 추적이 있으면 그 수 아래에 보기를 적습니다.

`text`·`info` 는 `.hwp`(HWP 5.0 바이너리)도 읽고, 같은 문서의 `.hwpx` 와 같은 위치 표기를 냅니다. 암호·DRM·배포용 문서는 복호하지 않고 에러(종료 코드 1)로 끝냅니다.

### apply

```sh
why-hwp apply -spec edits.json -o out.hwpx -author 이름 [-date RFC3339] [-no-track] [-force] in.hwpx|in.hwp
```

명세의 편집을 변경 추적으로 남긴 HWPX 사본을 만듭니다(`-no-track` 이면 추적 없이 고칩니다). 위치는 입력을 `why-hwp text` 로 읽었을 때의 loc 이고, 모든 연산이 입력 기준 번호를 씁니다. 문단이 여러 개인 칸은 `text` 가 보여 주는 대로 문단 위치(`s0/p3/t0/r1/c2/p1`)로 가리킵니다. 칸 위치만 주면 그 칸의 첫 문단입니다. `#box` 위치(글상자 안의 글)는 거절합니다. apply 는 글상자 안의 글을 고치지 못하고, 글상자가 놓인 문단 자체의 글만 고칩니다. 연산: `set_text` {loc, text}, `replace` {loc, find, with, all}, `insert_after` / `insert_before` {loc, paras:[{text, like}]}, `delete` {loc}, `image` {after 또는 loc, src(PNG), width_mm, like}. 새 문단은 `like`(없으면 `loc`) 문단의 문단·스타일·글자 모양을 따릅니다. 고치지 않은 문단은 바이트 그대로 두고, 메모를 지키며, PNG 는 손대지 않고 넣습니다. `.hwp` 입력은 내장 엔진으로 임시 폴더에 HWPX 를 만든 뒤 그 사본을 고칩니다. 출력은 늘 `.hwpx` 이고, 위치는 `.hwp` 파일을 `why-hwp text` 로 읽었을 때의 loc 입니다. 변환에서 문서의 일부가 빠질 수 있어서 apply 가 stderr 로 알려 주므로, 결과를 `why-hwp verify -orig in.hwp out.hwpx` 로 확인합니다. 암호·DRM·배포용 문서는 `text` 처럼 변환 전에 거부합니다. 예시: [`testdata/apply/spec.json`](testdata/apply/spec.json).

### verify

```sh
why-hwp verify [-orig 원본] [-allow locs.txt] [-banned words.txt] [-ignore-case] [-view accepted|original|markup] [-format plain|json] <파일>
```

문서를 넘기기 전에 확인합니다. `.hwpx`, `.hwp`, `.docx`, `.md`, `.txt` 를 읽고, md 와 txt 는 줄 단위로 `l1`, `l2`, ... 위치를 붙입니다. `-orig` 를 주면 바뀐 블록, 추가된 블록("added 2 after original s0/p7" 처럼), 삭제된 블록을 보여 줍니다. 원본의 메모, 표, 그림, 변경 추적이 사라졌거나 표가 작아졌으면 위반입니다. `-allow`(한 줄에 위치 하나, `s0/p4/t0/*` 는 접두사 일치, `s0/p4/t0/r1/c2` 같은 칸 위치는 그 칸의 문단들도 포함)를 주면 목록 밖의 변경은 위반입니다. 변경은 `text` 가 출력하는 위치 그대로 보고하므로, 문단이 여러 개인 칸은 문단별로 비교하고 글상자 안의 글은 `#box` 블록으로 따로 비교합니다. 금지어(`-banned`, 한 줄에 하나, `#` 주석, `re:` 는 정규식)와 em dash 는 위반이고, en dash 와 원본에 없던 숫자는 경고입니다. 위반이 있으면 종료 코드 1, 없으면 0, 사용법 오류나 파일을 못 읽으면 2 입니다. 두 문서 모두 `-view`(기본 `accepted`, `text` 참고)로 읽으므로, 변경 추적이 있는 hwpx·docx 는 지운 글을 빼고 넣은 글을 새 글로 비교합니다. 어느 보기로 읽었는지는 notes 에 나옵니다. hwp 는 보기를 지원하지 않아, 변경 추적이 있는 hwp 에서는 지운 글과 넣은 글을 구분하지 못합니다.

### convert

```sh
why-hwp convert [-to hwp|hwpx|pdf|txt] [-o 출력] [-engine builtin|hancom] [-timeout 10m] [-force] <입력.hwp|입력.hwpx|입력.docx>
```

문서를 HWP, HWPX, PDF, UTF-8 텍스트로 변환합니다. 형식은 `-to` 로 주거나 `-o` 의 확장자에서 정합니다. `-o` 가 없으면 입력 경로에서 확장자만 바꿔 쓰고, 출력이 입력 파일 자신이면 거부합니다. 출력 파일이 이미 있으면 `-force` 없이는 덮어쓰지 않습니다. `-timeout` 은 변환 한 번의 시간 상한입니다 (기본은 내장 엔진 10m, `-engine hancom` 120s).

기본(`-engine builtin`)은 why-hwp 안에 든 엔진으로 변환합니다. [rhwp](https://github.com/edwardkim/rhwp) 와 resvg 를 WebAssembly(WASI)로 컴파일해 [wazero](https://wazero.io) 로 실행하므로 모든 OS 에서 되고 다른 프로그램을 띄우지 않습니다. `.hwp` 와 `.hwpx` 를 읽고, `.docx` 는 오류를 내고 거부합니다. 처음 실행할 때 엔진을 컴파일하느라 몇 초(8코어 PC 에서 약 10초) 걸리고, 그 결과를 사용자 캐시 폴더(`why-hwp/wazero`)에 두어 다음부터는 빨리 시작합니다. 이 폴더에는 지금 버전의 컴파일 결과만 남고, 그것이 만들어지면 다른 why-hwp 버전의 것은 지웁니다. `-timeout` 은 변환과, PDF 출력이면 글꼴 내려받기의 시간 상한입니다. 첫 컴파일 시간도 `-timeout` 에 들어가므로, 컴파일보다 짧은 `-timeout` 은 상한에 닿는 즉시 실패하고 컴파일이 끝나지 않았으니 다음 실행에서 다시 컴파일합니다.

PDF 는 한글이 인쇄한 것과 똑같지 않은 근사입니다. 쪽 배치를 rhwp 가 직접 하므로 줄바꿈과 위치가 다를 수 있고, 문서의 글꼴이 없을 때 특히 그렇습니다. 문서가 쓰는 글꼴이 설치돼 있으면 더 가까워집니다. 같은 글꼴이 있는 PC 에서 한글 2024 가 인쇄한 것과 18개 시험 문서로 비교하면 15개가 맞았습니다. 13개는 모든 낱말이 같은 쪽에서 1pt 안에 놓였고, 한글의 PDF 에 글자 없이 그림으로만 그려진 2개(차트, 워터마크)는 쪽 그림을 비교해 맞았습니다. 2쪽 모아 찍기로 설정된 문서는 모든 쪽이 A4 세로이면 한글이 인쇄하듯 가로 한 장에 두 쪽이 나란히 놓인 PDF 가 됩니다. 같은 비교에서 엔진이 쓴 HWP·HWPX 파일은 18개 모두 한글로 인쇄했을 때 원본과 같았습니다.

글꼴은 PDF 출력에만 쓰입니다. HWP·HWPX·텍스트 출력은 글꼴을 읽지도 내려받지도 않으므로 글꼴이 있든 없든 같습니다. PDF 는 컴퓨터에 설치된 글꼴을 먼저 쓰고, 이 글꼴은 내려받거나 복사하지 않습니다. why-hwp 는 흔히 쓰는 글꼴 폴더를 읽습니다 (Windows: `%WINDIR%\Fonts`, `%LOCALAPPDATA%\Microsoft\Windows\Fonts`, 한컴오피스의 `Shared\TTF\All`·`Shared\TTF\Hwp`; macOS: `/System/Library/Fonts`, `/Library/Fonts`, `~/Library/Fonts`; Linux: `$XDG_DATA_HOME/fonts`(설정 안 했으면 `~/.local/share/fonts`), `~/.fonts`, `/usr/share/fonts`, `/usr/local/share/fonts`). 문서에 필요한데 설치돼 있지 않은 글꼴은 rhwp 의 공개 글꼴 목록에서 내려받습니다. 지정한 글꼴이 모두 없는 글자는 rhwp 가 정한 대체 글꼴(Noto Serif CJK KR, Noto Sans CJK KR, Noto Sans Mono CJK KR)로 그리는데, 이 중 설치되지 않은 것이 있으면 rhwp 의 공개 글꼴 Noto Sans KR 을 함께 내려받아 그 자리에 씁니다. 그래서 이런 글자가 PDF 에서 빠지지 않습니다. `WHY_HWP_FONT_DIRS` 는 먼저 찾을 폴더를 더합니다 (Windows 는 `;`, 그 밖은 `:` 로 나눕니다). `WHY_HWP_SYSTEM_FONTS=0` 이면 시스템 폴더를 읽지 않으므로, 출력이 내려받은 글꼴과 `WHY_HWP_FONT_DIRS` 에만 달려 있게 됩니다. 내려받은 글꼴은 사용자 캐시 폴더(`why-hwp/fonts`)에 두고 다시 받지 않습니다. `WHY_HWP_OFFLINE=1` 이면 아무것도 내려받지 않고, 없는 글꼴을 stderr 경고로 알린 뒤 PDF 를 대체 글꼴로 만듭니다. 어떤 글꼴을 어디서 받는지는 rhwp 의 목록을 따르고, 각 글꼴은 그 제작자의 라이선스를 따릅니다. 사용자 캐시 폴더는 Linux 에서 `~/.cache`(또는 `$XDG_CACHE_HOME`), macOS 에서 `~/Library/Caches`, Windows 에서 `%LOCALAPPDATA%` 입니다.

`-engine hancom` 은 설치된 한컴오피스로 변환합니다 (Windows 전용. 다른 OS 에서는 "convert needs Windows with Hancom Office" 를 내고 끝납니다). `.docx` 도 읽고, PDF 는 한글이 인쇄한 그대로입니다. 한글은 창을 숨긴 채 임시 폴더의 복사본으로 작업하므로 파일 접근 허용을 묻지 않습니다. 이 명령이 띄운 한글만 닫고, 시간이 넘으면 그 프로세스만 끝냅니다. 이미 열어 둔 한글 창은 건드리지 않습니다.

### svg2png

```sh
why-hwp svg2png [-width-mm N | -width-px N] [-dpi 600] [-trim] [-timeout 2m] [-o out.png] <in.svg>
```

SVG 를 문서에 들어갈 너비에 맞춘 PNG 로 만듭니다(기본 600 dpi, 높이는 SVG 비율대로). why-hwp 안에 든 resvg 로 그리므로 브라우저나 다른 프로그램이 필요 없고, 어느 PC 에서나 같은 PNG 가 나옵니다. 글자는 SVG 에 넣은 글꼴로만 그립니다. `<style>` 의 `@font-face` 에 `data:` URI 로 든 TrueType·OpenType 파일이 그것이고, `svgfont` 가 이렇게 넣습니다. 시스템에 설치된 글꼴은 쓰지 않으니 `svgfont` 를 먼저 돌립니다. `<text>` 가 있는데 넣은 글꼴이 없으면 글자 없이 그리고 stderr 에 경고를 냅니다. 풀 수 없거나 TrueType·OpenType 이 아닌(WOFF 등) 글꼴은 경고를 내고 건너뜁니다. `-trim` 은 흰 여백을 잘라냅니다. 너비를 주지 않으면 SVG 고유 크기(1인치 = 96px)를 씁니다. 출력은 한 변 20000px, 전체 1억 2천만 픽셀까지입니다. 처음 실행할 때 엔진을 컴파일하느라 몇 초 걸리고, 그 결과를 사용자 캐시 폴더(`why-hwp/wazero`, 지금 버전의 것만 남김)에 두어 다음부터는 빨리 시작합니다. `-timeout`(기본 2m)은 렌더의 시간 상한입니다. 첫 컴파일 시간도 `-timeout` 에 들어가므로, 컴파일보다 짧은 `-timeout` 은 상한에 닿는 즉시 실패하고 다음 실행에서 다시 컴파일합니다.

### svgfont

```sh
why-hwp svgfont [-regular font.ttf] [-bold font.ttf] [-family NAME] (-o out.svg | -w) <in.svg>
```

SVG 그림에 한글 폰트를 넣어, 그 폰트가 없는 PC 에서도 같게 보이게 합니다. `<text>`(안의 `<tspan>` 포함)에 쓴 글자만 넣습니다. 굵기마다 TrueType 부분 집합을 만들어 base64 로 `<defs>` 맨 앞의 `<style id="embedded-font">` 에 넣고, 모든 `font-family` 속성과 선언 맨 앞에 `'NAME'`(기본 `WhyHwpEmbed`)을 붙입니다. 그 요소나 가장 가까운 조상의 `font-weight` 가 bold 이거나 600 이상이면 굵은 폰트를 씁니다. 부분 집합의 name 테이블 이름도 `NAME` 과 Regular 또는 Bold(굵기 400 또는 700)로 바꾸므로, resvg 처럼 `@font-face` 를 무시하고 폰트 이름으로 찾는 렌더러도 이 폰트를 씁니다. 저작권·라이선스 이름은 그대로 둡니다. 이미 있는 `<style id="embedded-font">` 는 바꿔 넣으므로, 결과에 다시 돌려도 같은 파일이 나옵니다. base64 안에 클라우드 액세스 키처럼 보이는 부분(`AKIA` 와 영문·숫자 16자)이 생기면 커밋 훅이 비밀값으로 막기 때문에, 그 자리를 CSS 줄 이어쓰기(역슬래시와 줄바꿈)로 끊습니다. 폰트 데이터는 그대로입니다. stderr 에 굵기별로 폰트 파일, 글자 수, 부분 집합 크기를 보여 주고, 폰트에 없거나 빈 모양으로 나오는 글자를 알려 줍니다.

폰트는 TrueType(glyf 윤곽을 쓰는 `.ttf`)만 받습니다. CFF(`.otf`), 가변 폰트(`fvar` 표가 있는 것, 예: `NotoSansKR-VF.ttf`), 모음(`.ttc`), 라이선스가 넣기를 막는 폰트(OS/2 fsType 0x0002 비트)는 거절합니다. 굵기마다 플래그, `WHY_HWP_FONT_REGULAR` / `WHY_HWP_FONT_BOLD`, OS 폰트 폴더 검색 순서로 찾습니다. 검색하는 폴더는 Windows `%WINDIR%\Fonts`, `%LOCALAPPDATA%\Microsoft\Windows\Fonts`, Linux `/usr/share/fonts`, `/usr/local/share/fonts`, `~/.local/share/fonts`, `~/.fonts`(하위 폴더 포함), macOS `/Library/Fonts`, `~/Library/Fonts` 이고, 찾는 파일은 `NotoSansKR-Regular.ttf` / `NotoSansKR-Bold.ttf`, `NanumGothic.ttf` / `NanumGothicBold.ttf`, `NanumSquareR.ttf` / `NanumSquareB.ttf`, `malgun.ttf` / `malgunbd.ttf` 순서입니다. 마지막으로 why-hwp 폰트 캐시(사용자 캐시 폴더의 `why-hwp/fonts`, `convert` 가 내려받은 폰트를 두는 곳)를 봅니다. 캐시의 파일 이름은 폰트 이름이 아니므로, 여기서는 name 테이블의 패밀리 이름을 같은 순서(Noto Sans KR, NanumGothic, NanumSquare, Malgun Gothic)로, 굵기(400, 굵은 글꼴은 700)와 함께 맞춰 봅니다. rhwp 의 Noto Sans KR Bold 는 패밀리 이름이 Noto Sans KR Thin 으로 되어 있어 맞지 않습니다. 검색에서 나온 가변·CFF·넣기 제한 파일은 건너뜁니다. 아무것도 없으면 Google Fonts 저장소의 고정 커밋(`https://cdn.jsdelivr.net/gh/google/fonts@2fe1e8c6b137fb13041a7cfca6fdcd8a910ea4dc/ofl/nanumgothic/`)에서 나눔고딕(NanumGothic) Regular 또는 Bold 를 내려받아, why-hwp 에 고정한 SHA-256 과 맞춰 본 뒤 폰트 캐시의 `svgfont/NanumGothic-Regular.ttf` / `svgfont/NanumGothic-Bold.ttf` 에 둡니다. 다음부터는 캐시에서 찾으므로 다시 받지 않고, 받을 때는 무엇을 받았는지 stderr 에 한 줄 보여 줍니다. 나눔고딕은 넣기를 허용하는 정적 TrueType 폰트이고 SIL Open Font License 1.1(같은 폴더의 `OFL.txt`)을 따릅니다. 이 라이선스는 폰트를 넣는 것과 소프트웨어와 함께 재배포하는 것을 허용합니다. "Nanum", "NanumGothic" 은 예약 폰트 이름(Reserved Font Name)인데, 넣는 부분 집합은 이름을 `NAME` 으로 바꾸므로 이 이름을 쓰지 않습니다. `WHY_HWP_OFFLINE=1` 이면 내려받지 않고 오류에 그 사실을 적습니다. 받기에 실패하면 그 이유를 오류에 덧붙입니다. 다른 폰트를 쓰려면 Noto Sans KR 정적 TrueType 폰트를 설치하거나 `-regular`(굵은 글자가 있으면 `-bold` 도)를 줍니다.

### svgcheck

```sh
why-hwp svgcheck [-regular font.ttf] [-bold font.ttf] [-format plain|json] [-tol 2] <in.svg>
```

SVG 그림의 배치를 계산으로 검사합니다. 글자 폭은 `svgfont` 와 같은 방법으로 찾은 폰트의 advance 폭으로 재고, `font-size`, `text-anchor`, `tspan` 줄을 반영합니다. 위반은 넷입니다. 글자가 자기 상자(글자의 `x`, `y` 를 담은 가장 작은 `<rect>`, 테두리 선 두께만큼 안쪽이 좁아짐) 안쪽을 넘침, 서로 다른 `<text>` 의 글자끼리 겹침, 화살표 끝이 어느 상자 테두리에서도 `-tol` px 넘게 떨어짐(화살표는 `marker-start` 나 `marker-end` 가 있는 `line`, `polyline`, `path` 이고 양쪽 끝을 다 봄), 화살표가 자기 끝이 없는 상자 안을 지나감입니다. 화살표 끝 셋 이상이 3 x `-tol` px 안에 몰리면 화살촉이 별처럼 보이므로 경고입니다. 곡선·호가 있는 path 는 검사하지 않고, transform 은 `translate` 만 따라갑니다. 건너뛴 것은 notes 에 나옵니다. 위치는 요소 id, 없으면 요소 이름과 문서 순서 번호(`text#3`, `line#7`)에 글자 앞부분을 붙여 보여 줍니다. 위반이 있으면 종료 코드 1, 없으면 0(경고는 허용), 사용법 오류나 파일을 못 읽거나 쓸 폰트가 없으면 2 입니다.

## 빌드

Go 1.25 이상과 GNU make 가 필요합니다. Windows 에서는 Git Bash 에서 make 를 실행합니다.

```sh
make            # 현재 OS 용으로 bin/ 에 빌드
make dist       # 6개 플랫폼을 dist/why-hwp-<os>-<arch>[.exe] 로 빌드
make checksums  # dist/ 를 빌드하고 dist/SHA256SUMS 생성
make test       # go test ./...
```

`why-hwp version` 이 출력하는 버전은 `git describe --tags --always --dirty` 에서 옵니다. `make VERSION=v1.0.0` 으로 바꿀 수 있습니다.

주석 달린 `v*` 태그를 push 하면 `.github/workflows/release.yml` 이 테스트하고 6개 플랫폼을 빌드한 뒤, 바이너리와 `SHA256SUMS` 를 붙이고 태그 메시지를 노트로 쓴 GitHub 릴리즈를 만듭니다.

## 라이선스

[Apache License 2.0](LICENSE)

내장 엔진(`internal/engine/engine.wasm.gz`)에는 rhwp(MIT), resvg(Apache-2.0 또는 MIT), tiny-skia(BSD-3-Clause), Independent JPEG Group 의 작업에서 온 코드(IJG License), Latin Modern Math 글꼴의 일부(GUST Font License)와 그 밖의 Rust crate 가 들어 있습니다. 라이선스 전문과 crate 목록은 [`wasm/LICENSES/`](wasm/LICENSES/) 에 있습니다. 실행 중에 내려받는 글꼴은 why-hwp 에 들어 있지 않으며 각자의 라이선스를 따릅니다.
