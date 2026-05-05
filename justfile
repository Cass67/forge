help:
    @just --list

commit := `git rev-parse --short HEAD`

build:
    go build -ldflags "-X forge/internal/version.Commit={{commit}}" -o ./bin/forge ./cmd/forge

run:
    ./bin/forge

test:
    go test ./...

check: build test

test-pkg PKG:
    go test {{PKG}}

pre-commit:
    pre-commit run --all-files
