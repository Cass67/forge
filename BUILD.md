# Building Forge

This document covers local builds and cross-compilation.

It does not cover packaging or distribution systems such as Homebrew, Scoop, `.deb`, `.rpm`, notarized macOS apps, or signed Windows installers.

## Requirements

- Go `1.25.x`
- `just` installed and available on your `PATH`
- a working C toolchain is not required for the current default build

For local development and commit hooks, see [LOCAL_TOOLING.md](/Users/cass/git/forge/LOCAL_TOOLING.md).

## Primary Local Workflow

From the repository root, use the `just` recipes first:

```bash
just build
```

Run the binary:

```bash
just run
```

Run tests:

```bash
just test
```

Run the combined build-and-test check:

```bash
just check
```

For hook-compatible verification, use:

```bash
just pre-commit
```

## Underlying Go Commands

`just build` wraps the standard local build:

```bash
go build -o ./bin/forge ./cmd/forge
```

`just test` wraps the repository test suite:

```bash
go test ./...
```

`just check` runs the same build-and-test sequence as the recipes above.

## Common Local Builds

### macOS

Apple Silicon:

```bash
GOOS=darwin GOARCH=arm64 go build -o ./bin/forge-darwin-arm64 ./cmd/forge
```

Intel macOS:

```bash
GOOS=darwin GOARCH=amd64 go build -o ./bin/forge-darwin-amd64 ./cmd/forge
```

### Linux

x86_64:

```bash
GOOS=linux GOARCH=amd64 go build -o ./bin/forge-linux-amd64 ./cmd/forge
```

arm64:

```bash
GOOS=linux GOARCH=arm64 go build -o ./bin/forge-linux-arm64 ./cmd/forge
```

### Windows

x86_64:

```bash
GOOS=windows GOARCH=amd64 go build -o ./bin/forge-windows-amd64.exe ./cmd/forge
```

arm64:

```bash
GOOS=windows GOARCH=arm64 go build -o ./bin/forge-windows-arm64.exe ./cmd/forge
```

## Cross-Compilation Matrix

A simple multi-target build sequence:

```bash
mkdir -p bin

GOOS=darwin  GOARCH=arm64 go build -o ./bin/forge-darwin-arm64      ./cmd/forge
GOOS=darwin  GOARCH=amd64 go build -o ./bin/forge-darwin-amd64      ./cmd/forge
GOOS=linux   GOARCH=amd64 go build -o ./bin/forge-linux-amd64       ./cmd/forge
GOOS=linux   GOARCH=arm64 go build -o ./bin/forge-linux-arm64       ./cmd/forge
GOOS=windows GOARCH=amd64 go build -o ./bin/forge-windows-amd64.exe ./cmd/forge
GOOS=windows GOARCH=arm64 go build -o ./bin/forge-windows-arm64.exe ./cmd/forge
```

If you want smaller shell snippets, treat these as the primary target set:

- `darwin/arm64`
- `darwin/amd64`
- `linux/amd64`
- `linux/arm64`
- `windows/amd64`
- `windows/arm64`

## Optional Build Flags

You can add common Go release flags if you want a smaller artifact:

```bash
go build -trimpath -ldflags="-s -w" -o ./bin/forge ./cmd/forge
```

Use that only if it helps your local workflow. It is not required for normal development.

## Verification

Before treating a build as release-ready, run:

```bash
just check
```

If you are preparing code for commit, also expect the repo hooks to run:

- `gofmt`
- `goimports`
- `go vet`
- `golangci-lint`
- `govulncheck`
- `gitleaks`

If you want to mirror commit behavior more closely, run:

```bash
just pre-commit
```

## Build Output Conventions

Recommended local output names:

- `forge-darwin-arm64`
- `forge-darwin-amd64`
- `forge-linux-amd64`
- `forge-linux-arm64`
- `forge-windows-amd64.exe`
- `forge-windows-arm64.exe`

## One Binary For Every Platform?

No. Forge cannot realistically be compiled once into a single native binary that runs unchanged on macOS, Linux, and Windows.

What Go gives you is easy cross-compilation, not a universal native executable.

You still need one binary per:

- operating system
- CPU architecture

For Forge specifically, that is the right model:

- it is a native terminal application
- it integrates with OS filesystems, terminals, paths, and process execution
- it is not a JVM, browser, or WASM app

So the practical deployment story is:

- compile once per target
- distribute one artifact per target OS/arch pair

## Troubleshooting

### `go build` fails on version mismatch

Check your Go version:

```bash
go version
```

Forge currently declares Go `1.25.0` in [go.mod](/Users/cass/git/forge/go.mod).

### Hooks fail on commit but build succeeds

The build only proves the binary compiles. The repo hooks also enforce formatting, linting, vulnerability checks, and secret scanning.

See [LOCAL_TOOLING.md](/Users/cass/git/forge/LOCAL_TOOLING.md) for the required local tools.

### Cross-compiled binary starts but behaves differently

Double-check the platform assumptions around:

- path separators
- shell commands used by tools
- terminal capabilities
- approval and TUI behavior on that target OS

### Built binary works on one machine but not another

Check:

- target OS
- target architecture
- executable bit on Unix-like systems
- whether you built the correct artifact for that machine
