GO ?= go
BIN := bin/minigit
ifeq ($(OS),Windows_NT)
  BIN := bin/minigit.exe
endif

.PHONY: all build test check vet fmt clean

all: build

build:
	$(GO) build -o $(BIN) .

test:
	$(GO) test ./... -count=1

check: vet
	$(GO) test -race ./... -count=1

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

clean:
	rm -rf bin
