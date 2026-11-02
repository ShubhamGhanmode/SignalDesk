package migrations

import "embed"

//go:embed *.sql *.cql
var Files embed.FS
