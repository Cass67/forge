default: help

help:
    @just --list

build:
    go build -o ./bin/forge ./cmd/forge

run: build
    ./bin/forge

test:
    go test ./...

check:
    go build ./...
    go test ./...

test-pkg PKG:
    go test {{PKG}}

pre-commit:
    pre-commit run --all-files
