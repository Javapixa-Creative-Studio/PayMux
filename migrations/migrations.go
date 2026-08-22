// Package migrations embeds PayMux's SQL schema migrations so a binary can
// bring its own database up to date without shipping loose files.
package migrations

import "embed"

// FS holds every migration, applied in lexical filename order.
//
//go:embed *.sql
var FS embed.FS
