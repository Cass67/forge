help:
    @just --list

commit := `git rev-parse --short HEAD`
bindir := env_var_or_default("FORGE_BINDIR", home_directory() / ".local/bin")

# Build and install everything — CLI, desktop app, macOS bundle
all: install install-gui
    @echo
    @echo "forge      -> {{bindir}}/forge"
    @echo "forge-gui  -> {{bindir}}/forge-gui"

# Build the CLI
build:
    go build -ldflags "-X forge/internal/version.Commit={{commit}}" -o ./bin/forge ./cmd/forge

# The desktop app is a separate binary from the CLI because Wails needs CGO,
# which would otherwise cost the CLI its clean cross-compilation.

# Build the desktop app
gui: web
    go build -ldflags "-X forge/internal/version.Commit={{commit}}" -o ./bin/forge-gui ./cmd/forge-gui

# Install the CLI
install: build
    @mkdir -p {{bindir}}
    install -m 0755 ./bin/forge {{bindir}}/forge

# On macOS the .app bundle is what gives the app its icon, name and Spotlight
# entry; a bare binary cannot have one.

# Install the desktop app
install-gui: gui
    #!/usr/bin/env bash
    set -euo pipefail
    mkdir -p {{bindir}}
    install -m 0755 ./bin/forge-gui {{bindir}}/forge-gui
    if [ "$(uname)" != "Darwin" ]; then
        exit 0
    fi
    ./build/macapp.sh >/dev/null
    dest=/Applications
    [ -w "$dest" ] || dest="$HOME/Applications"
    mkdir -p "$dest"
    rm -rf "$dest/Forge.app"
    cp -R bin/Forge.app "$dest/Forge.app"
    echo "Forge.app  -> $dest/Forge.app"

# Run the CLI
run: build
    ./bin/forge

# Run the desktop app
run-gui: gui
    ./bin/forge-gui

# Run the test suite
test:
    go test ./...

# Build and test
check: build test

# Test a single package
test-pkg PKG:
    go test {{PKG}}

# Run the pre-commit hooks over everything
pre-commit:
    pre-commit run --all-files

# Compiles the frontend into web/dist, which the desktop binary embeds.
[private]
web:
    cd web && bun install && bun run build

# macOS .app bundle on its own, without installing it.
[private]
app: gui
    ./build/macapp.sh

# Regenerates the icon assets. Runs as part of the bundle build.
[private]
icon:
    go run ./build/icongen build
