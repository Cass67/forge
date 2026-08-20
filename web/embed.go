// Package web exposes the built frontend assets for embedding into the
// forge binary. Rebuild with: cd web && npm ci && npm run build
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
