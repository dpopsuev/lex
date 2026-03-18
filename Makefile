.PHONY: build build-image push-image run restart version release test test-e2e test-llm

VERSION ?= $(shell git describe --tags --always --dirty)

version:
	@echo $(VERSION)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION)" -o bin/lex ./cmd/lex

build-image:
	podman build --build-arg VERSION=$(VERSION) \
		-t quay.io/dpopsuev/lex:$(VERSION) \
		-t quay.io/dpopsuev/lex:latest \
		-f Dockerfile.test .

push-image:
	podman push quay.io/dpopsuev/lex:$(VERSION)
	podman push quay.io/dpopsuev/lex:latest

LEX_DATA ?= $(HOME)/.lex

run:
	podman rm -f lex 2>/dev/null || true
	podman run -d --name lex -p 8082:8082 --userns=keep-id \
		-v $(HOME):$(HOME):ro \
		-v $(LEX_DATA):/data:Z \
		quay.io/dpopsuev/lex:latest serve --transport http --addr :8082
	@sleep 1 && podman logs lex 2>&1 | tail -3

restart: build-image run

test:
	go test ./...

test-e2e:
	go test -tags e2e -v -timeout 120s .

test-llm:
	go test -tags llm -v -timeout 600s .

release:
	@test -n "$(V)" || (echo "usage: make release V=v0.5.0" && exit 1)
	sed -i 's|quay.io/dpopsuev/lex:[^ "]*|quay.io/dpopsuev/lex:$(V)|g' README.md
	git add README.md && git commit -m "release: $(V)" || true
	git tag $(V)
	$(MAKE) build-image VERSION=$(V)
	git push origin main --tags
