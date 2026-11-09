// Package migrations embeds the SQL migrations, applied in file-name order by
// goose (internal/store.Migrate).
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
