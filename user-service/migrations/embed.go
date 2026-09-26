// Package migrations embeds the versioned SQL migrations so the binary can
// apply them on startup without shipping the .sql files separately.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
