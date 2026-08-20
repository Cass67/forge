help:
    @just --list

commit := `git rev-parse --short HEAD`

build:
    go build -ldflags "-X forge/internal/version.Commit={{commit}}" -o ./bin/forge ./cmd/forge

run:
    ./bin/forge

# The desktop app. Built separately from the CLI because Wails needs CGO,
# which would otherwise cost the CLI its clean cross-compilation.
gui: web
    go build -ldflags "-X forge/internal/version.Commit={{commit}}" -o ./bin/forge-gui ./cmd/forge-gui

run-gui: gui
    ./bin/forge-gui

# macOS .app bundle, so the app gets its icon, name and Cmd-Tab entry.
app: gui
    ./build/macapp.sh

# Regenerates the icon assets from build/icongen.
icon:
    go run ./build/icongen build

# Compiles the frontend into web/dist, which both binaries embed.
web:
    cd web && bun install && bun run build

install: build
    install -m 0755 ./bin/forge ~/.local/bin/forge

install-gui: gui
    install -m 0755 ./bin/forge-gui ~/.local/bin/forge-gui

all: install

test:
    go test ./...

check: build test

test-pkg PKG:
    go test {{PKG}}

pre-commit:
    pre-commit run --all-files
