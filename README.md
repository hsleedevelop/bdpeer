# bdpeer

![Release](https://github.com/hsleedevelop/bdpeer/actions/workflows/release.yml/badge.svg)
![Go Version](https://img.shields.io/github/go-mod/go-version/hsleedevelop/bdpeer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hsleedevelop/bdpeer)](https://goreportcard.com/report/github.com/hsleedevelop/bdpeer)

같은 네트워크 또는 인터넷을 통해 피어를 자동으로 발견하고 텍스트 메시지와 파일을 주고받는 크로스 플랫폼 P2P TUI 앱.

```
┌──────────────────────────────────────────────────────┐
│  bdpeer  [alice]                                     │
├──────────────┬───────────────────────────────────────┤
│ Peers        │ Chat: bob                             │
│              │                                       │
│ ▶ bob        │ bob: 안녕!                            │
│   carol      │ you: 파일 보낼게                      │
│              │                                       │
│              │ > /file ~/photo.jpg█                  │
│ ↑/↓ select  │ Enter send  /file <path>  Esc quit   │
└──────────────┴───────────────────────────────────────┘
```

## 특징

- **자동 피어 발견** — 같은 Wi-Fi(mDNS/Bonjour) 또는 다른 네트워크(libp2p DHT)에서 설정 없이 탐색
- **수동 연결** — `/connect <multiaddr>` 로 주소를 직접 입력해 연결
- **텍스트 채팅** — 선택한 피어에게 실시간 메시지 전송
- **파일 전송** — `/file <경로>` 명령으로 파일 전송, SHA-256 체크섬 검증
- **안정적인 identity** — peer ID가 앱 재시작 후에도 유지됨
- **자체 업데이트** — `bdpeer --update` 한 줄로 최신 버전으로 교체
- **크로스 플랫폼** — macOS, Windows, Linux 지원

## 설치

[Releases](https://github.com/hsleedevelop/bdpeer/releases) 페이지에서 플랫폼에 맞는 바이너리를 다운로드합니다.

### macOS

```bash
# 압축 해제
tar -xzf bdpeer_*_darwin_*.tar.gz

# Gatekeeper 차단 해제 (미서명 바이너리)
xattr -d com.apple.quarantine bdpeer

# 실행
./bdpeer
```

> 시스템 환경설정 → 개인 정보 보호 및 보안에서 "어쨌든 허용"을 눌러도 됩니다.

### Linux

```bash
tar -xzf bdpeer_*_linux_*.tar.gz
chmod +x bdpeer
./bdpeer
```

### Windows

zip 압축 해제 후 `bdpeer.exe` 실행.

### 직접 빌드

```bash
# 현재 플랫폼 빌드
make build

# 실행
./dist/bdpeer
```

## 사용법

```bash
./bdpeer              # TUI 실행
./bdpeer --help       # 도움말
./bdpeer --version    # 버전 확인
./bdpeer --update     # 최신 버전으로 업데이트
```

첫 실행 시 닉네임을 입력하면 메인 화면으로 전환됩니다. 같은 네트워크의 피어는 즉시 발견되고, 다른 서브넷·인터넷 너머 피어는 DHT를 통해 약 10–30초 후 자동으로 나타납니다.

`Tab` 키로 우측 패널을 로그 뷰로 전환하면 피어 발견 과정(DHT 광고·검색·연결 시도)을 실시간으로 확인할 수 있습니다.

| 키 / 명령 | 동작 |
|---|---|
| `↑` / `↓` | 피어 선택 |
| `Enter` | 메시지 전송 |
| `Tab` | 채팅 ↔ 로그 패널 전환 |
| `/connect <multiaddr>` | 주소로 피어 직접 연결 |
| `/file <경로>` | 파일 전송 |
| `Ctrl+C` | 종료 |

### 다른 서브넷 피어와 연결하기

상단 타이틀에 표시된 내 주소를 상대방에게 전달하면 수동 연결이 가능합니다.

```
 bdpeer  [alice]  /ip4/192.168.50.71/tcp/12345/p2p/12D3KooW...
```

상대방 입력창에서:

```
/connect /ip4/192.168.50.71/tcp/12345/p2p/12D3KooW...
```

## 빌드

```bash
make build          # 현재 플랫폼
make build-mac      # macOS (amd64 + arm64)
make build-win      # Windows amd64
make build-linux    # Linux amd64

# BLE 지원 포함 빌드 (CGo 필요)
make build-mac-ble
make build-linux-ble
make build-win-ble
```

## 피어 발견 프로토콜

| 프로토콜 | 지원 환경 | 설명 |
|---|---|---|
| mDNS/Bonjour (`_bdpeer._tcp`) | macOS, iOS, Android, Windows 10+ | 로컬 Wi-Fi 자동 발견 |
| SSDP/UPnP | Windows, Android | Windows 네트워크 탐색 |
| WS-Discovery | Windows | 탐색기 → 네트워크 폴더 표시 |
| BLE | macOS, Linux, Windows (`-tags ble`) | 근거리 근접 발견 |
| libp2p DHT | 인터넷 | 서브넷이 달라도 자동 발견 (시작 후 ~10초) |

> **AirDrop**: Apple 전용 AWDL 프로토콜 사용 — 구현 불가. 같은 Wi-Fi에서 Bonjour로 발견 가능.  
> **Quick Share**: Google Nearby Connections 와이어 프로토콜 필요 (Phase 3 예정).  
> **DHT**: 공개 IPFS DHT 네트워크(`bdpeer/v1` 네임스페이스)를 사용. 양측 모두 인터넷 접근이 가능해야 합니다. NAT 홀펀칭(DCUtR)을 지원하여 서로 다른 공유기(NAT) 뒤에 있는 경우에도 연결을 시도합니다.

## 기술 스택

- [Go 1.22+](https://golang.org/)
- [libp2p](https://libp2p.io/) — P2P 네트워킹
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI 프레임워크
- [zeroconf](https://github.com/grandcat/zeroconf) — mDNS/Bonjour
- [go-ssdp](https://github.com/koron/go-ssdp) — SSDP/UPnP
- [tinygo bluetooth](https://github.com/tinygo-org/bluetooth) — BLE (`-tags ble`)

## 라이선스

[MIT](LICENSE)
