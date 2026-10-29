package migrations

import "embed"

// Files contains the ordered database migrations.
//
//go:embed *.sql
var Files embed.FS
