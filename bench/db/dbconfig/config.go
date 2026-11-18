// Package dbconfig holds the database section of deploy/bench.yaml. It is a
// leaf package so lib can carry the section without linking the SQL drivers;
// db.Config is an alias of Config.
package dbconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Drivers.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// Defaults.
const (
	DefaultPath   = ".heimdall/heimdall.db" // sqlite, relative to runs_root
	DefaultDSNEnv = "HEIMDALL_DATABASE_URL"
)

// Config is the `database:` block.
//
//	database:
//	  driver: sqlite              # sqlite | postgres
//	  path: .heimdall/heimdall.db # sqlite, relative to runs_root
//	  dsn_env: HEIMDALL_DATABASE_URL   # postgres
type Config struct {
	Driver string `yaml:"driver" json:"driver,omitempty"`
	// Path is the SQLite file. Relative paths resolve against runs_root
	// (see Resolve).
	Path string `yaml:"path" json:"path,omitempty"`
	// DSNEnv names the environment variable holding the Postgres DSN. The
	// DSN itself is never written in the config file.
	DSNEnv string `yaml:"dsn_env" json:"dsn_env,omitempty"`
	// MaxOpenConns caps the pool; zero picks a driver default.
	MaxOpenConns int `yaml:"max_open_conns" json:"max_open_conns,omitempty"`

	// DSN is a Postgres DSN given directly (tests, callers that resolved it
	// already). It wins over DSNEnv. Never read from YAML.
	DSN string `yaml:"-" json:"-"`
}

// Resolve fills defaults (driver sqlite, path .heimdall/heimdall.db, dsn_env
// HEIMDALL_DATABASE_URL) and makes a relative SQLite path absolute against
// runsRoot. runsRoot should itself be absolute.
func (c Config) Resolve(runsRoot string) Config {
	c.Driver = strings.ToLower(strings.TrimSpace(c.Driver))
	if c.Driver == "" {
		c.Driver = SQLite
	}
	if c.Driver == SQLite {
		if c.Path == "" {
			c.Path = DefaultPath
		}
		if !filepath.IsAbs(c.Path) && runsRoot != "" {
			c.Path = filepath.Join(runsRoot, c.Path)
		}
	}
	if c.DSNEnv == "" {
		c.DSNEnv = DefaultDSNEnv
	}
	return c
}

// Validate checks the section without touching the database.
func (c Config) Validate() error {
	switch c.Driver {
	case "", SQLite:
		if strings.ContainsRune(c.Path, '?') {
			return fmt.Errorf("database.path %q: must not contain '?'", c.Path)
		}
	case Postgres:
	default:
		return fmt.Errorf("database.driver must be sqlite or postgres, got %q", c.Driver)
	}
	if c.MaxOpenConns < 0 {
		return fmt.Errorf("database.max_open_conns must not be negative")
	}
	return nil
}

// PostgresDSN returns the DSN: Config.DSN when set, else the value of the
// environment variable named by DSNEnv.
func (c Config) PostgresDSN() (string, error) {
	if c.DSN != "" {
		return c.DSN, nil
	}
	env := c.DSNEnv
	if env == "" {
		env = DefaultDSNEnv
	}
	dsn := strings.TrimSpace(os.Getenv(env))
	if dsn == "" {
		return "", fmt.Errorf("database.driver postgres: environment variable %s is empty", env)
	}
	return dsn, nil
}
