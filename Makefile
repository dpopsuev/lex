.PHONY: build install version release test test-e2e test-llm

VERSION ?= $(shell git describe --tags --always --dirty)

version:
	@echo $(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION)" -o bin/lex ./cmd/lex

install:
	go install -ldflags="-s -w -X main.Version=$(VERSION)" ./cmd/lex

test:
	go test ./...

test-e2e:
	go test -tags e2e -v -timeout 120s .

test-llm:
	go test -tags llm -v -timeout 600s .

release:
	@test -n "$(V)" || (echo "usage: make release V=v0.5.0" && exit 1)
	git tag $(V)
	git push origin main --tags
