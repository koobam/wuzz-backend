// Package migrations embeds every service's SQL migrations into the migrate
// binary, so a deployed image carries its own migrations with nothing extra
// to copy alongside it.
package migrations

import "embed"

//go:embed user/*.sql catalog/*.sql transaction/*.sql gateway/*.sql
var FS embed.FS
