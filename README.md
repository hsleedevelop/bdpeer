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
- **닉네임 연결** — `/connect <닉네임>` 으로 DHT에서 상대방을 검색해 연결
- **수동 연결** — `/connect <multiaddr>` 로 주소를 직접 입력해 연결
- **텍스트 채팅** — 선택한 피어에게 실시간 메시지 전송
- **파일 전송** — `/file <경로>` 명령으로 파일 전송, SHA-256 체크섬 검증
- **안정적인 identity** — peer ID가 앱 재시작 후에도 유지됨
- **자체 업데이트** — `bdpeer --update` 한 줄로 최신 버전으로 교체
- **크로스 플랫폼** — macOS, Windows, Linux 지원

## 설치

[Releases](https://github.com/hsleedevelop/bdpeer/releases) 페이지에서 플랫폼에 맞는 바이너리를 다운로드합니다.

### macOS

Apple Silicon은 `darwin_arm64`, Intel은 `darwin_amd64` 아카이브를 받습니다. **반드시 자기 아키텍처에 맞는 바이너리를 사용해야 합니다** — Rosetta 2로 amd64를 실행하면 CoreBluetooth TCC 경로에서 크래시가 발생합니다.

```bash
# 1. 다운로드 (Apple Silicon 예시 — Intel은 darwin_amd64로 교체)
VERSION=0.5.17
curl -L -o bdpeer.tar.gz \
  https://github.com/hsleedevelop/bdpeer/releases/download/v${VERSION}/bdpeer_${VERSION}_darwin_arm64.tar.gz

# 2. 압축 해제 + Gatekeeper 격리 속성 제거
tar -xzf bdpeer.tar.gz
xattr -d com.apple.quarantine bdpeer 2>/dev/null

# 3. 서명 검증 (선택)
codesign -dv ./bdpeer 2>&1 | grep -E "Identifier|flags"
# Identifier=dev.hsleedevelop.bdpeer
# CodeDirectory ... flags=0x2(adhoc) ...

# 4. 실행 — 반드시 Terminal.app 또는 iTerm2에서 (cmux 등 비호환 멀티플렉서 금지)
./bdpeer
```

> 시스템 환경설정 → 개인 정보 보호 및 보안에서 "어쨌든 허용"을 눌러도 됩니다.

#### Bluetooth 권한 (BLE 사용 시 필수)

최초 실행 시 시스템에서 **"Terminal이/iTerm이 Bluetooth에 접근하려 합니다"** 다이얼로그가 뜹니다. 허용해야 근거리 피어 발견(BLE)이 동작합니다.

- bdpeer는 ad-hoc 코드서명 + 임베디드 Info.plist(`NSBluetoothAlwaysUsageDescription`) 구성으로 배포됩니다. Hardened Runtime은 macOS 26 + Go cgo 조합에서 SIGABRT를 유발해 사용하지 않습니다.
- v0.5.13부터 macOS는 기본적으로 BLE GATT 데이터 채널만 사용하며, BLE→WebRTC 업그레이드는 `enable_ble_webrtc: true` 설정에서만 활성화됩니다. Windows↔Windows BLE 발견을 위해 v0.5.10의 광고 경로도 복원했습니다.
- v0.5.12부터 macOS CoreBluetooth 초기화 직후 닉네임 객체 수명 문제로 발생하던 `dataUsingEncoding:` 크래시를 수정했습니다.
- macOS의 TCC는 child process가 아닌 **호스트 터미널 앱**의 권한을 확인합니다. cmux, tmux 일부 빌드 등 Bluetooth usage description이 없는 터미널 멀티플렉서에서 실행하면 다이얼로그가 뜨지 않고 즉시 종료됩니다. 이 경우 Terminal.app 또는 iTerm2에서 실행해 주세요.
- 권한 다이얼로그를 거부했거나 동작이 이상하다면: 시스템 설정 → 개인 정보 보호 및 보안 → Bluetooth 에서 사용 중인 터미널 앱을 토글로 켜면 됩니다.

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
./bdpeer --debug      # 현재 디렉토리에 bdpeer-debug.log 기록
./bdpeer --help       # 도움말
./bdpeer --version    # 버전 확인
./bdpeer --update     # 최신 버전으로 업데이트
```

`--debug` 모드는 현재 작업 디렉토리(`$PWD`)에 `bdpeer-debug.log` 파일을 만들고 모든 코어 이벤트(로그·피어 발견/유실·메시지·파일 전송 진행·오류)를 타임스탬프와 함께 append 합니다. TUI 종료 후에도 파일이 남아 재현/분석에 활용할 수 있습니다.

첫 실행 시 닉네임을 입력하면 메인 화면으로 전환됩니다. 같은 네트워크의 피어는 즉시 발견되고, 다른 서브넷·인터넷 너머 피어는 DHT를 통해 약 10–30초 후 자동으로 나타납니다. BLE 근거리 피어는 `s` 키를 눌러 5초 동안 검색합니다.

`Tab` 키로 우측 패널을 로그 뷰로 전환하면 피어 발견 과정(DHT 광고·검색·연결 시도)을 실시간으로 확인할 수 있습니다.

| 키 / 명령 | 동작 |
|---|---|
| `←` / `→` | 패널 이동 (피어 / 채팅 / 파일) — 활성 패널은 보더가 강조됨 |
| `↑` / `↓` | 패널 내 항목 선택 (피어 / 파일 / 메시지 스크롤) |
| `Enter` | 메시지 전송 · 디렉토리 진입 · 파일 전송 확인 |
| `Tab` | 채팅 ↔ 로그 패널 전환 |
| `1` / `2` / `3` | 피어 / 채팅 / 파일 패널로 바로 이동 |
| `s` | BLE 피어 5초 검색 |
| `/connect <닉네임>` | 닉네임으로 피어 검색 후 연결 (DHT) |
| `/connect <multiaddr>` | 주소로 피어 직접 연결 |
| `/file <경로>` | 파일 전송 |
| `Ctrl+C` / `Esc` / `q` | 종료 |

파일 패널에서는 긴 파일명은 자동 절단되고 `↑ 12/45 ↓` 형태의 스크롤 인디케이터가 표시됩니다. 파일 전송 시 채팅 패널 하단에 시각적 진행률 바(`[████████░░░░] 67%`)가 송·수신 양쪽에 표시됩니다. v0.5.3부터 송신측은 모든 바이트를 송출한 뒤 수신측의 FILE_ACK가 도착할 때까지 `송출 완료 · 수신 대기 중...` 상태로 표시되어, transport 버퍼만 비운 상태와 실제 전송 완료를 구분합니다.

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
make build          # 현재 플랫폼 (BLE 포함)
make build-mac      # macOS arm64 + amd64 (CGO=1, BLE 포함)
make build-win      # Windows amd64 (BLE GATT 포함)
make build-linux    # Linux amd64 (BLE 스캔 포함)
```

darwin 빌드는 `codesign` 단계가 자동 포함됩니다 — ad-hoc 서명. Apple Developer ID 인증서 없이도 동작하며, `internal/discovery/Info.plist`가 `__TEXT,__info_plist` 섹션에 임베드되어 macOS TCC가 `NSBluetoothAlwaysUsageDescription`을 읽을 수 있습니다. Hardened Runtime은 macOS 26 + Go cgo 조합에서 BLE 초기화 직후 SIGABRT (`VM - Waiting on busy page was interrupted`)를 유발해 활성화하지 않습니다.

## 피어 발견 프로토콜

| 프로토콜 | 지원 환경 | 설명 |
|---|---|---|
| mDNS/Bonjour (`_bdpeer._tcp`) | macOS, iOS, Android, Windows 10+ | 로컬 Wi-Fi 자동 발견 |
| SSDP/UPnP | Windows, Android | Windows 네트워크 탐색 |
| WS-Discovery | Windows | 탐색기 → 네트워크 폴더 표시 |
| BLE GATT | macOS (기본 내장, CoreBluetooth) | BLE 발견 + DataChar로 직접 메시지 송수신. WebRTC 업그레이드는 설정으로 명시 활성화 |
| BLE GATT | Linux (기본 내장, tinygo) | BLE 발견 + GATT DataChar로 직접 메시지 송수신 |
| BLE GATT | Windows (기본 내장, tinygo) | BLE GATT로 근거리 발견 + 직접 텍스트 송수신. v0.5.11부터 Windows↔Windows는 `/connect` 없이도 BLE 세션 ID로 라우팅 |
| libp2p DHT | 인터넷 | 서브넷이 달라도 자동 발견 (시작 후 ~10초) |

> **SSDP 자동 연결 (v0.4.3+)**: SSDP 광고에 libp2p peer.ID가 포함되어, 같은 LAN의 Windows/Android 피어가 발견되면 별도 `/connect` 없이도 libp2p로 자동 연결됩니다. 발견 즉시 양방향 텍스트·파일 전송 가능.  
> **BLE→WebRTC**: macOS에서 `enable_ble_webrtc: true`로 명시 활성화한 경우에만 사용합니다. BLE로 상대를 발견하면 WebRTC SDP offer/answer를 BLE로 교환하고 STUN/TURN으로 NAT를 뚫어 직접 연결합니다. 알파벳 순으로 낮은 닉네임이 Initiator(offer), 높은 닉네임이 Responder(answer)로 자동 결정됩니다. v0.3.5부터 ICE gathering 타임아웃 시 연결을 끊지 않고 수집된 candidate로 핸드셰이크를 계속 진행하여 기업망 등 STUN/TURN 응답이 느린 환경에서도 연결 성공률이 향상됩니다. v0.4.3부터 BLE 발견 즉시 사이드바에 잠정 항목으로 표시되어 WebRTC 핸드셰이크 진행 상황을 바로 확인할 수 있습니다.
> **BLE 데이터 전송 (v0.4+)**: BLE GATT DataChar(`BD9E0004`)로 텍스트 프레임을 직접 송수신합니다. macOS↔macOS는 기본적으로 BLE GATT만으로 통신합니다. macOS↔Linux도 BLE GATT만으로 통신합니다. Windows↔Windows는 v0.5.11부터 SessionChar(`BD9E0005`)와 session-addressed `S` chunk를 사용해 `ble-<session>` synthetic ID로 라우팅합니다. 이 경로는 양쪽 Windows가 v0.5.11 이상이어야 하며, 구버전 Windows 또는 macOS/Linux와의 BLE direct 호환은 별도 fallback 작업 대상입니다. v0.5.14부터 BLE 스캔은 상시 실행하지 않고 `s` 키를 누른 뒤 5초 동안만 실행합니다. v0.5.15부터는 5초 스캔 창 전체를 유지하고 발견 후보를 모두 연결해, 첫 광고 하나 때문에 다른 피어 검색이 중단되지 않습니다. v0.5.16부터는 BLE synthetic ID의 원문 라우팅 키를 보존해, 목록에는 보이지만 전송 시 `unknown peer`가 나는 문제를 수정했습니다. v0.5.17부터 macOS 스캔은 중간 실패로 남은 stale peripheral 캐시를 재처리하고 CoreBluetooth 광고/스캔/연결 로그를 남깁니다. v0.5.2부터 BLE 연결이 중간에 끊어져도 재조립 버퍼가 자동으로 초기화되어 다음 메시지가 오염되지 않습니다(이전 버전에서 `invalid character '\x00'` 에러로 나타나던 frame desync 해결).
> **TURN 릴레이**: STUN만으로 NAT 홀펀칭이 실패하면(기업망 등) Open Relay Project TURN 서버가 자동으로 중계합니다. 전송 데이터는 DTLS로 암호화되어 TURN 서버도 내용을 볼 수 없습니다. 자체 TURN 서버를 사용하려면 아래 설정을 참고하세요.  
> **AirDrop**: Apple 전용 AWDL 프로토콜 — 구현 불가. 같은 Wi-Fi에서는 Bonjour로 발견 가능.  
> **Quick Share**: Google Nearby Connections 와이어 프로토콜 필요 (Phase 3 예정).  
> **DHT**: 공개 IPFS DHT(`bdpeer/v1` 네임스페이스) 사용. NAT 홀펀칭(DCUtR) + AutoRelay 지원.

## TURN 서버 설정 (선택)

BLE→WebRTC를 활성화하면 STUN 실패 시 Open Relay Project TURN 서버를 자동으로 사용합니다. 자체 TURN 서버(coturn 등)를 사용하려면 config 파일에 추가하세요.

**config 파일 경로**

| 플랫폼 | 경로 |
|---|---|
| macOS | `~/Library/Application Support/bdpeer/config.json` |
| Linux | `~/.config/bdpeer/config.json` |
| Windows | `%APPDATA%\bdpeer\config.json` |

```json
{
  "nickname": "alice",
  "enable_ble_webrtc": true,
  "turn_servers": [
    {
      "url": "turn:my-coturn.example.com:3478",
      "username": "user",
      "credential": "password"
    }
  ]
}
```

`turn_servers`를 설정하면 Open Relay 대신 지정한 서버만 사용합니다.

## 기술 스택

- [Go 1.22+](https://golang.org/)
- [libp2p](https://libp2p.io/) — P2P 네트워킹
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — TUI 프레임워크
- [zeroconf](https://github.com/grandcat/zeroconf) — mDNS/Bonjour
- [go-ssdp](https://github.com/koron/go-ssdp) — SSDP/UPnP
- [tinygo bluetooth](https://github.com/tinygo-org/bluetooth) — BLE 스캔 + GATT central/server (Linux DataChar 송수신, Windows GATT 세션 라우팅)
- [CoreBluetooth](https://developer.apple.com/documentation/corebluetooth) — BLE peripheral+central+WebRTC 시그널링+DataChar 데이터 전송, macOS
- [pion/webrtc](https://github.com/pion/webrtc) — WebRTC STUN/TURN NAT traversal (`v4`)

## 라이선스

[MIT](LICENSE)
