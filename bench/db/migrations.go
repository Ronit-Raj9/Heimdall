package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/core/*.sql
var coreMigrations embed.FS

//go:embed migrations/index/*.sql
var indexMigrations embed.FS

func init() {
	Register(CoreSet, mustSub(coreMigrations, "migrations/core"))
	Register("index", mustSub(indexMigrations, "migrations/index"))
}

func mustSub(f embed.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
