.PHONY: build build-mac build-win build-linux test

# darwin: CGO_ENABLED=1 (CoreBluetooth BLE)
build:
	CGO_ENABLED=1 go build -o dist/bdpeer ./cmd/bdpeer

build-mac:
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -o dist/bdpeer-mac-arm64 ./cmd/bdpeer
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -o dist/bdpeer-mac-amd64 ./cmd/bdpeer

# linux/windows: tinygo BLE scan, CGO not required
build-win:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/bdpeer-windows-amd64.exe ./cmd/bdpeer

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dist/bdpeer-linux-amd64 ./cmd/bdpeer

test:
	go test ./...
