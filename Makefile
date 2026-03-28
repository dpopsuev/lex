.PHONY: build install version release test test-e2e test-llm fmt vet lint lint-new preflight install-hooks

VERSION ?= $(shell git describe --tags --always --dirty)

version:
	@echo $(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION)" -o bin/lex ./cmd/lex

install:
	go install -ldflags="-s -w -X main.Version=$(VERSION)" ./cmd/lex

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

lint-new:
	golangci-lint run --new-from-rev=HEAD ./...

preflight: fmt vet lint test

install-hooks:
	@echo '#!/bin/sh' > .git/hooks/pre-commit
	@echo 'make lint-new' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed (runs make lint-new)"

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
