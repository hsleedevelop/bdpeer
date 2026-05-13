.PHONY: build build-mac build-win build-linux build-mac-ble build-linux-ble build-win-ble

build:
	go build -o dist/bdpeer ./cmd/bdpeer

build-mac:
	GOOS=darwin GOARCH=amd64 go build -o dist/bdpeer-mac-amd64 ./cmd/bdpeer
	GOOS=darwin GOARCH=arm64 go build -o dist/bdpeer-mac-arm64 ./cmd/bdpeer

build-win:
	GOOS=windows GOARCH=amd64 go build -o dist/bdpeer-windows-amd64.exe ./cmd/bdpeer

build-linux:
	GOOS=linux GOARCH=amd64 go build -o dist/bdpeer-linux-amd64 ./cmd/bdpeer

build-mac-ble:
	GOOS=darwin GOARCH=arm64 go build -tags ble -o dist/bdpeer-mac-arm64-ble ./cmd/bdpeer

build-linux-ble:
	GOOS=linux GOARCH=amd64 go build -tags ble -o dist/bdpeer-linux-amd64-ble ./cmd/bdpeer

build-win-ble:
	GOOS=windows GOARCH=amd64 go build -tags ble -o dist/bdpeer-windows-amd64-ble.exe ./cmd/bdpeer

test:
	go test ./...
