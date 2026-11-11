# refcompare

[English](README.md)

변환기 출력이 한글(Hwp)과 얼마나 같은지 재는 개발용 도구다. `why-hwp` 실행 파일에는 들어가지 않는다.

- `ref` 는 원격 Windows PC 의 한글로 기준 출력을 만든다.
- `cmp` 는 후보 출력(어느 변환기가 만들었든)을 기준과 비교해 표를 낸다.

## 필요한 것

| 어디 | 무엇 |
|------|------|
| 원격 Windows PC | 한글 (Hwp 2024 로 확인), Windows 실행이 켜진 WSL (`/proc/sys/fs/binfmt_misc/WSLInterop`), WSL 안의 ssh 서버 |
| 이 PC | Go, 원격에 키로 로그인되는 `ssh`·`scp` (`BatchMode=yes` 라 비밀번호를 묻지 않는다), poppler-utils 의 `pdftotext` |

원격은 환경 변수로만 받는다.

| 변수 | 뜻 |
|------|----|
| `WHY_HWP_REF_HOST` | ssh 대상. 예: `user@host` |
| `WHY_HWP_REF_DIR` | Windows 에서도 보이는 `/mnt/<드라이브>/` 아래 WSL 폴더. 예: `/mnt/c/Users/<나>/why-hwp-ref`. 도구는 `<dir>/refcompare` 안에서만 일하고 그 밖은 건드리지 않는다 |

## ref: 기준 만들기

```sh
go build -o /tmp/refcompare ./tools/refcompare
WHY_HWP_REF_HOST=user@host WHY_HWP_REF_DIR=/mnt/c/Users/me/why-hwp-ref \
  /tmp/refcompare ref -corpus CORPUS -out OUT
```

- `CORPUS` 는 `.hwp`·`.hwpx` 파일이 든 폴더다 (하위 폴더는 읽지 않는다).
- `<name>`(확장자까지 포함한 파일 이름)마다 한글로 `OUT/ref/<name>.pdf`, `<name>.txt`, 그리고
  다른 형식(hwp 입력이면 `<name>.hwpx`, hwpx 입력이면 `<name>.hwp`)을 만든다.
- 변환은 Windows 용으로 빌드한 `why-hwp convert`(한글 COM)다. 기본값은 지금 폴더의 모듈을
  커밋 안 된 변경까지 포함해 빌드한다. 커밋된 상태로 하려면 직접 빌드해 `-exe` 로 넘긴다:
  `git archive HEAD | tar -x -C /tmp/w && (cd /tmp/w && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o why-hwp.exe ./cmd/why-hwp)`.
- 실행 파일은 원격 사본과 SHA-256 이 다를 때만 복사한다. 입력은 해시로 만든 이름으로 복사하므로
  파일 이름이 원격 셸에 닿지 않는다.
- `OUT/ref/manifest.json` 에 출력마다 입력 해시를 적는다. 같은 입력으로 만든 출력이 있으면
  다시 만들지 않는다. `-force` 는 전부 다시 만든다.
- ssh 호출은 모두 `-n`, `BatchMode=yes` 이고 시간 제한이 있다. `-timeout`(기본 120s)은 변환 한 번에
  주는 한글의 제한 시간이다.
- 한글이 빈 텍스트 파일을 저장하면(차트만 있는 문서처럼 글자가 없는 문서) 기준 `.txt` 를 빈 파일로 둔다.

## 후보

`cmp` 는 후보를 만들지 않는다. `<name>.pdf`, `<name>.txt`, 그리고 `<name>.hwpx`(hwp 입력) 또는
`<name>.hwp`(hwpx 입력)가 든 폴더를 읽는다. 없는 파일은 표에 적고 만들지 않는다.

rhwp 명령줄 도구(rhwp 저장소에서 빌드)로는 이렇게 만든다.

```sh
for f in CORPUS/*.hwp CORPUS/*.hwpx; do
  n=$(basename "$f")
  rhwp export-pdf "$f" -o "CAND/$n.pdf"
  rm -rf /tmp/txt && rhwp export-text "$f" -o /tmp/txt && cat /tmp/txt/*.txt > "CAND/$n.txt"
  case "$n" in
    *.hwp)  rhwp export-hwpx "$f" "CAND/$n.hwpx" ;;
    *.hwpx) rhwp convert "$f" "CAND/$n.hwp" ;;
  esac
done
```

`export-text` 는 쪽마다 파일 하나(`<base>_001.txt`, ...)를 쓴다. 이름 순서로 이어 붙인다.

## cmp: 비교

```sh
/tmp/refcompare cmp -out OUT -cand CAND [-tol 1.0] [-fit scale] [-skip-memo] [-reconvert]
```

표는 stdout 과 `OUT/report.md` 에 나온다. 종료 코드는 모든 문서가 통과하면 0, 하나라도 실패하거나
비교하지 못하면 1, 사용법이 틀리면 2 다.

| 옵션 | 뜻 |
|------|----|
| `-tol` | 허용하는 낱말 가장자리 최대 편차 (pt, 기본 1.0) |
| `-fit` | 재기 전에 기준 쪽을 후보 쪽에 맞추는 방법: `scale`(같은 비율 축척 + 이동, 기본), `shift`(이동만), `none` |
| `-skip-memo` | 한글의 메모 영역에 있는 기준 낱말을 뺀다 (아래 참고) |
| `-reconvert` | 후보 HWP/HWPX 를 원격 한글로 PDF 로 인쇄해(`OUT/reconv/`, `ref` 처럼 캐시) 기준 PDF 와도 비교한다. `ref` 와 같은 환경 변수가 필요하다. `-exe`·`-force`·`-timeout` 은 `ref` 와 같다 |

### PDF 비교 방법

1. `pdftotext -bbox` 로 낱말과 그 상자를 얻는다.
2. 낱말을 읽는 순서(쪽, 위에서부터 줄, 왼쪽부터)로 놓는다. 두 PDF 는 글자를 쓰는 순서가 달라서
   내용 스트림 순서는 쓰지 않는다.
3. 같은 낱말을 순서대로 짝짓는다(최장 공통 부분열). 짝지은 두 낱말 사이에 남은 글자는 글자 단위로
   다시 짝지어서, 한쪽만 낱말을 쪼갠 경우(`온·오` 와 `온·` + `오`)도 짝이 된다. 그래도 양쪽에 남은
   같은 글자의 낱말은 가장 가까운 위치끼리 짝짓는다. 읽는 순서가 다른 낱말(표 칸)이나 다른 쪽으로
   넘어간 낱말이 여기서 잡힌다.
4. 같은 자리의 다른 글자가 짧게(2자까지) 이어질 때, 기준 글자가 기호 글꼴 코드(사용자 정의 영역,
   또는 Wingdings 글머리표의 `Ÿ` 같은 코드 페이지 1252 바이트)이고 후보 글자가 문자·숫자가 아니면
   *치환*으로 본다. 문자나 숫자가 바뀐 것은 치환이 아니다.
5. 두 PDF 가 같은 글자에서 낱말 경계를 가질 때만 가장자리를 잰다. 짝지은 글자로 시작하는 낱말의
   왼쪽 가장자리, 짝지은 글자로 끝나는 낱말의 오른쪽 가장자리다. 가장자리 점은 (x, 낱말 상자의
   세로 가운데)다.
6. 쪽 쌍마다 가장자리로 `-fit` 의 맞춤을 구한다(최소제곱, 첫 맞춤에서 멀리 떨어진 점을 빼고 한 번 더).
   가장자리가 6개보다 적은 쪽은 이동만 맞춘다. 한글은 메모 말풍선 자리를 내려고 본문을 줄이므로
   축척이 필요하다.
7. 가장자리 편차는 맞춘 뒤 가로·세로 거리 중 큰 값이다 (후보 pt).

`-skip-memo` 를 주면, 짝이 없는 기준 낱말 가운데 그 쪽이 줄어들었고(맞춤 축척 1.01 초과) 맞춘 뒤
그 쪽의 모든 후보 낱말보다 오른쪽에 놓이는 것을 뺀다. 한글이 메모를 찍는 자리다.

### 판정

문서는 다음을 모두 만족하면 통과한다.

- 쪽 수가 같다.
- 짝지은 낱말이 다른 쪽 번호에 있지 않다.
- 짝 없는 낱말이 (양쪽 모두) 없다.
- 가장자리 최대 편차가 `-tol` 이하다.

`-reconvert` 를 주면, 한글로 인쇄한 후보 HWP/HWPX 도 기준 PDF 에 대해 같은 시험을 통과해야 한다.

판정에 넣지 않고 보여만 주는 것: 치환(`subst`), 맞춤 축척, TXT 비교. TXT 는 빈 줄과 줄 끝 공백을
빼고 `&#NNNN;`(한글이 자기 코드 페이지 밖 글자를 이렇게 쓴다)을 풀어서 줄 단위로 비교한다. 두 변환기는
표와 공백을 텍스트로 옮기는 방식이 달라서 참고용이다.

보고서의 `## Differences` 에는 실패한 문서마다 편차가 가장 큰 낱말, 짝 없는 낱말의 앞 몇 개, 치환의
앞 몇 개를 적는다.

### 한계

- 세로 가운데는 글꼴 상자에서 나온다. 두 PDF 가 다른 글꼴로 그리면 기준선이 같아도 가운데가 움직인다.
- 외곽선으로 그린 글자(한글은 차트 글자를 패스로 그린다)는 낱말이 없다. 그러면 후보의 차트 글자가
  짝 없는 낱말로 나온다.
- 한글은 문서의 인쇄 설정(예: 모아 찍기 2쪽)을 따른다. 후보는 따르지 않으므로 쪽 수가 달라진다.
