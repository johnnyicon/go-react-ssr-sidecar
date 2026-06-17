// Package static embeds the compiled React frontend.
// In development, dist/ contains only a .gitkeep placeholder.
// Run `npm run build` in frontend/ then ensure dist/ is copied here before `go build`.
// The Dockerfile handles this automatically via the multi-stage build.
package static

import "embed"

//go:embed all:dist
var FS embed.FS
