// Package migrations embeds SQL migration files into the binary so the
// application can run them at startup without shipping a separate directory
// alongside the distroless image.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
