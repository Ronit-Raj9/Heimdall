package lib

import "github.com/maximhq/bifrost/bench/db/dbconfig"

// DatabaseConfig is the database section with defaults filled (Database
// area, docs/platform-design.md): driver sqlite at
// <runs_root>/.heimdall/heimdall.db, or postgres with the DSN read from the
// environment variable named by dsn_env (default HEIMDALL_DATABASE_URL).
// Pass it to db.Open.
func (c *Config) DatabaseConfig() dbconfig.Config {
	return c.Database.Resolve(c.Path(c.RunsRoot))
}
