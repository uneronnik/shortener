package migrations

import "embed"

// Files contains the SQL migrations bundled with the migration command.
//
//go:embed *.sql
var Files embed.FS
