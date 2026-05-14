# bdpeer

Cross-platform P2P TUI for local file & message transfer.

## Usage
```
./bdpeer
```

## Discovery protocols
- **mDNS/Bonjour** (`_bdpeer._tcp`) — local WiFi, discovered by iOS/macOS/Android/Windows
- **SSDP/UPnP** — Windows Network Discovery, some Android apps
- **WS-Discovery** — Windows Explorer → Network folder
- **BLE** — proximity discovery (build with `-tags ble`)
- **libp2p DHT** — initial internet peer discovery (relay reservation is a future task ⚠️)

## Build
```
make build         # current platform
make build-mac     # macOS (amd64 + arm64)
make build-win     # Windows amd64
make build-linux   # Linux amd64
make build-mac-ble # macOS ARM64 with BLE support
```

## Notes
- **Internet P2P**: DHT is initialized but circuit relay bootstrap/reservation is not implemented. Internet-facing P2P requires explicit relay work.
- **AirDrop**: Uses Apple proprietary AWDL, not implementable. Discoverable via Bonjour on same WiFi.
- **Quick Share**: Requires Google Nearby Connections wire protocol (future work).
