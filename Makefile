DATE=$(shell date '+%F %T')
BRANCH=$(shell git branch --show-current)
COMMIT=$(shell git rev-parse HEAD | cut -c1-7)
NAME=gt
EXE=$(shell go env GOEXE)
VERSION=$(NAME) - $(DATE) - $(BRANCH) $(COMMIT)
RELEASE_OPTIONS=-trimpath
OPTIONS=-trimpath -race
LDFLAGS=-ldflags "-s -w -X 'github.com/isrc-cas/gt/predef.Version=$(VERSION)'"
SOURCES=$(shell ls **/*.go)

.PHONY: fmt test revive clean

all: fmt revive test release

fmt:
	go fmt ./...

test:
	go test -race -cover ./...

revive:
	revive -config revive.toml -exclude bufio/bufio.go -exclude logger/file-rotatelogs/... -exclude build/... -exclude release/... -formatter unix ./...

build: build_server build_client

release: release_server release_client

build_client: $(SOURCES) Makefile
	$(eval NAME=client)
	go build $(OPTIONS) $(LDFLAGS) -o build/$(NAME)$(EXE) ./cmd/client

release_client: $(SOURCES) Makefile
	$(eval NAME=client)
	go build -tags release $(RELEASE_OPTIONS) $(LDFLAGS) -o release/$(NAME)$(EXE) ./cmd/client

build_server: $(SOURCES) Makefile
	$(eval NAME=server)
	go build $(OPTIONS) $(LDFLAGS) -o build/$(NAME)$(EXE) ./cmd/server

release_server: $(SOURCES) Makefile
	$(eval NAME=server)
	go build -tags release $(RELEASE_OPTIONS) $(LDFLAGS) -o release/$(NAME)$(EXE) ./cmd/server

clean:
	rm build/* release/*
