package web

import "embed"

// Assets contains the production Vite build. Run npm run build in web first.
//
//go:embed all:dist
var Assets embed.FS
