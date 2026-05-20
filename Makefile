.PHONY: build build-mac build-win build-linux test

# darwin: CGO_ENABLED=1 (CoreBluetooth BLE).
# ad-hoc codesign is required for macOS TCC to read NSBluetoothAlwaysUsageDescription
# from the binary's embedded Info.plist (linked via -sectcreate in ble_darwin.go).
# Without a stable code identity, CoreBluetooth crashes the process on first use.
CODESIGN_ARGS=--force --sign - --timestamp=none --options runtime --entitlements internal/discovery/entitlements.plist

build:
	CGO_ENABLED=1 go build -o dist/bdpeer ./cmd/bdpeer
	codesign $(CODESIGN_ARGS) dist/bdpeer

build-mac:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o dist/bdpeer-mac-arm64 ./cmd/bdpeer
	codesign $(CODESIGN_ARGS) dist/bdpeer-mac-arm64
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o dist/bdpeer-mac-amd64 ./cmd/bdpeer
	codesign $(CODESIGN_ARGS) dist/bdpeer-mac-amd64

# linux/windows: tinygo BLE scan, CGO not required
build-win:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/bdpeer-windows-amd64.exe ./cmd/bdpeer

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/bdpeer-linux-amd64 ./cmd/bdpeer

test:
	go test ./...
