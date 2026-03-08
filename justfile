default:
    @just --list

build:
    go build -o bin/lex ./cmd/lex

test:
    go test ./...

install:
    go install ./cmd/lex
