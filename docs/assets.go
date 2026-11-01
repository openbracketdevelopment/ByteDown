package docs

import "embed"

//go:embed api.md openapi.yaml reference.html reference.css reference.js
var Assets embed.FS
