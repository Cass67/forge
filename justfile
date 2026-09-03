help:
    @just --list

commit := `git rev-parse --short HEAD`
bindir := env_var_or_default("FORGE_BINDIR", home_directory() / ".local/bin")

# Build and install everything — CLI, desktop app, macOS bundle
#
# Both binaries are built before either is installed. Installing as we went
# meant a GUI build failure still left a freshly installed CLI, and the two
# drifted months apart without anyone noticing.
all: build gui install install-gui
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
# entry; a bare binary cannot have one. The bundle carries both executables:
# forge-gui is its entrypoint and forge remains available as the CLI/TUI.

# Install the desktop app
install-gui: build gui
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
    cd web && bun test

# Build and test
check: build test

# Test a single package
test-pkg PKG:
    go test {{PKG}}

# Run the pre-commit hooks over everything
pre-commit:
    pre-commit run --all-files

# Compiles the frontend into web/dist, which the desktop binary embeds.
#
# web/dist is built, not committed: it was 3.7 MB of minified output that
# churned on every GUI change. A previous build is reused when bun is missing
# or the build fails, so a broken toolchain does not cost you an install, but
# an empty web/dist is fatal — there is nothing to embed.
[private]
web:
    #!/usr/bin/env bash
    set -uo pipefail
    if ! bun --version >/dev/null 2>&1; then
        echo "web: no usable bun; reusing the existing web/dist" >&2
    elif ! (cd web && bun install && bun run build); then
        echo "web: frontend build failed; reusing the existing web/dist" >&2
    else
        exit 0
    fi
    if [ ! -f web/dist/index.html ]; then
        echo "web: web/dist is empty and cannot be rebuilt; install bun and run 'just gui'" >&2
        exit 1
    fi

# macOS .app bundle on its own, without installing it.
[private]
app: build gui
    ./build/macapp.sh

# Regenerates the icon assets. Runs as part of the bundle build.
[private]
icon:
    go run ./build/icongen build
