// Package lib holds what the bench HTTP server shares across handlers: the
// configuration loaded from deploy/bench.yaml, the middlewares and the JSON
// response helpers. It mirrors transports/bifrost-http/lib in Bifrost.
package lib

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// EnvPrefix starts every environment override: BENCH_<SECTION>_<KEY>.
const EnvPrefix = "BENCH"

// Config is deploy/bench.yaml.
type Config struct {
	RunsRoot       string           `yaml:"runs_root" json:"runs_root"`
	BenchmarksRoot string           `yaml:"benchmarks_root" json:"benchmarks_root"`
	Pricing        string           `yaml:"pricing" json:"pricing"`
	Server         ServerConfig     `yaml:"server" json:"server"`
	Gateway        GatewayConfig    `yaml:"gateway" json:"gateway"`
	FaultProxy     FaultProxyConfig `yaml:"faultproxy" json:"faultproxy"`
	Networks       NetworksConfig   `yaml:"networks" json:"networks"`
	Images         ImagesConfig     `yaml:"images" json:"images"`

	Root string `yaml:"-" json:"-"` // bench root that relative paths resolve against
}

// ServerConfig configures the bench API server.
type ServerConfig struct {
	Listen       string       `yaml:"listen" json:"listen"`
	Mode         schemas.Mode `yaml:"mode" json:"mode"`
	AllowedHosts []string     `yaml:"allowed_hosts" json:"allowed_hosts"`
	CORSOrigins  []string     `yaml:"cors_origins" json:"cors_origins"`
}

// GatewayConfig points at the stock Bifrost gateway.
type GatewayConfig struct {
	URL           string   `yaml:"url" json:"url"`
	AgentURL      string   `yaml:"agent_url" json:"agent_url"`
	AdminTokenEnv string   `yaml:"admin_token_env" json:"admin_token_env"`
	Provider      string   `yaml:"provider" json:"provider"`
	KeySlots      []string `yaml:"key_slots" json:"key_slots"`
}

// FaultProxyConfig points at the fault proxy control API.
type FaultProxyConfig struct {
	ControlURL string `yaml:"control_url" json:"control_url"`
}

// NetworksConfig names the Docker networks the bench creates.
type NetworksConfig struct {
	Eval string `yaml:"eval" json:"eval"`
}

// ImagesConfig names the trusted helper images.
type ImagesConfig struct {
	Helper string `yaml:"helper" json:"helper"`
}

// LoadConfig reads the YAML file at path, applies BENCH_<SECTION>_<KEY>
// environment overrides and validates the result. Relative paths in the file
// resolve against the bench root: the parent of the folder holding the file,
// so deploy/bench.yaml resolves against bench/.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := applyEnv(reflect.ValueOf(cfg).Elem(), EnvPrefix, os.LookupEnv); err != nil {
		return nil, fmt.Errorf("apply environment to config %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path %s: %w", path, err)
	}
	cfg.Root = filepath.Dir(filepath.Dir(abs))
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks the fields the server cannot run without.
func (c *Config) Validate() error {
	switch c.Server.Mode {
	case schemas.ModeLive, schemas.ModeRecorded, schemas.ModeSynthetic:
	default:
		return fmt.Errorf("server.mode must be live, recorded or synthetic, got %q", c.Server.Mode)
	}
	if _, _, err := net.SplitHostPort(c.Server.Listen); err != nil {
		return fmt.Errorf("server.listen %q: %w", c.Server.Listen, err)
	}
	if c.RunsRoot == "" {
		return fmt.Errorf("runs_root is empty")
	}
	return nil
}

// Path resolves p against the bench root unless it is already absolute.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) || c.Root == "" {
		return p
	}
	return filepath.Join(c.Root, p)
}

// ListenIsLoopback reports whether server.listen binds a loopback address,
// the bench API's first defence (DESIGN.md 14).
func (c *Config) ListenIsLoopback() bool {
	host, _, err := net.SplitHostPort(c.Server.Listen)
	if err != nil {
		return false
	}
	return IsLoopbackHost(host)
}

// IsLoopbackHost reports whether host names the loopback interface.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// applyEnv walks a config struct and overrides every field whose environment
// variable is set. Nested structs extend the prefix with their YAML key, so
// server.listen reads BENCH_SERVER_LISTEN. Lists are comma-separated.
func applyEnv(v reflect.Value, prefix string, lookup func(string) (string, bool)) error {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		key := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		name := prefix + "_" + strings.ToUpper(key)
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct {
			if err := applyEnv(fv, name, lookup); err != nil {
				return err
			}
			continue
		}
		raw, ok := lookup(name)
		if !ok {
			continue
		}
		if err := setFromEnv(fv, raw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// setFromEnv parses raw into one scalar or string-list field.
func setFromEnv(fv reflect.Value, raw string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("parse bool %q: %w", raw, err)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return fmt.Errorf("parse int %q: %w", raw, err)
		}
		fv.SetInt(n)
	case reflect.Slice:
		if fv.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported list type %s", fv.Type())
		}
		var items []string
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		fv.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("unsupported field type %s", fv.Type())
	}
	return nil
}
