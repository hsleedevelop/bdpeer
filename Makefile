.PHONY: build build-mac build-win build-linux

build:
	go build -o dist/bdpeer ./cmd/bdpeer

build-mac:
	GOOS=darwin GOARCH=amd64 go build -o dist/bdpeer-mac-amd64 ./cmd/bdpeer
	GOOS=darwin GOARCH=arm64 go build -o dist/bdpeer-mac-arm64 ./cmd/bdpeer

build-win:
	GOOS=windows GOARCH=amd64 go build -o dist/bdpeer-windows-amd64.exe ./cmd/bdpeer

build-linux:
	GOOS=linux GOARCH=amd64 go build -o dist/bdpeer-linux-amd64 ./cmd/bdpeer

test:
	go test ./...
