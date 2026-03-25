// Package assets provides embedded static files for production builds.
// In development mode, files are served from disk for instant changes.
package assets

import "embed"

//go:embed css/output.css js/*.js js/*.min.js
var Files embed.FS
