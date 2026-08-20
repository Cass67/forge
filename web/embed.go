// Package web exposes the built frontend assets for embedding into the
// forge binary. Rebuild with: cd web && bun install && bun run build
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
