VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
export CGO_ENABLED := 0

.PHONY: all linux windows test clean

all: linux windows

linux:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='$(LDFLAGS)' -o dist/bandwave-linux-amd64 .
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags='$(LDFLAGS)' -o dist/bandwave-linux-arm64 .

windows:
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='$(LDFLAGS)' -o dist/bandwave.exe .
	GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='$(LDFLAGS) -H=windowsgui' -o dist/bandwave-agent.exe .

test:
	go vet ./...
	GOOS=windows go vet ./...
	CGO_ENABLED=1 go test -race ./...

clean:
	rm -rf dist bandwave
