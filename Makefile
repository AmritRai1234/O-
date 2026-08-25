BINARY := bin/o+
VERSION := v0.1.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: build test vet clean install

build:
	go build -ldflags "-X github.com/amritrai/oplus/internal/version.Version=$(VERSION) -X github.com/amritrai/oplus/internal/version.Commit=$(COMMIT)" -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin dist

install: build
	install -m 0755 $(BINARY) $(HOME)/.local/bin/o+
