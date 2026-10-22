VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test vet lint install clean

build:
	go build -ldflags "$(LDFLAGS)" -o bin/ivanti-neurons ./cmd/ivanti-neurons

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/ivanti-neurons

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

clean:
	rm -rf bin dist
