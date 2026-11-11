// Package db embeds the goose migrations, so binaries and tests apply the
// same history that `mise run migrate` does.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the migration files, rooted at the migrations directory.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}
