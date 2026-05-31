BINARY := forktrust
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test lint fmt clean release-snapshot

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/forktrust

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/forktrust

test:
	go test ./...

lint:
	go vet ./...
	gofmt -l . | tee /dev/stderr | (! read)

fmt:
	gofmt -w .

clean:
	rm -rf $(BINARY) dist/

release-snapshot:
	goreleaser release --snapshot --clean
