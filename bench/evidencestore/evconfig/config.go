// Package evconfig holds the storage section of deploy/bench.yaml. It is a
// leaf package so lib can carry the section without linking the evidence
// store or the S3 client.
package evconfig

import (
	"fmt"
	"os"
	"strings"
)

// Defaults.
const (
	DefaultEndpointEnv  = "HEIMDALL_S3_ENDPOINT"
	DefaultAccessKeyEnv = "HEIMDALL_S3_ACCESS_KEY"
	DefaultSecretKeyEnv = "HEIMDALL_S3_SECRET_KEY"
	DefaultRegion       = "us-east-1"
	DefaultSweepMinutes = 15
)

// Config is the `storage:` block.
//
//	storage:
//	  root: runs                  # hosted: per-org folders under runs/orgs/<org_id>/
//	  mirror_dir: /mnt/backup/heimdall   # a local folder mirror, or:
//	  s3:                         # an S3-compatible mirror
//	    endpoint_env: HEIMDALL_S3_ENDPOINT
//	    bucket: heimdall-evidence
//	    access_key_env: HEIMDALL_S3_ACCESS_KEY
//	    secret_key_env: HEIMDALL_S3_SECRET_KEY
//	  sweep_interval_m: 15        # server sweeper; negative switches it off
//
// Secrets are named by environment variable, never written in the file.
type Config struct {
	// Root is where hosted mode keeps per-organization folders
	// (runs/orgs/<org_id>/); empty means runs_root. Read by tenancy.
	Root string `yaml:"root" json:"root,omitempty"`
	// MirrorDir is a local folder used as the mirror (a second disk or a
	// network share). Relative paths resolve against the bench root.
	MirrorDir string   `yaml:"mirror_dir" json:"mirror_dir,omitempty"`
	S3        S3Config `yaml:"s3" json:"s3,omitzero"`
	// SweepIntervalM is how often the server's sweeper mirrors every
	// experiment, in minutes: 0 means 15, negative switches the sweep off
	// (the seal hook still runs).
	SweepIntervalM int `yaml:"sweep_interval_m" json:"sweep_interval_m,omitempty"`
	// Concurrency is the number of parallel transfers; 0 means 4.
	Concurrency int `yaml:"concurrency" json:"concurrency,omitempty"`
}

// S3Config is an S3-compatible bucket: AWS S3, MinIO, Cloudflare R2,
// Backblaze B2 or anything else that speaks SigV4.
type S3Config struct {
	EndpointEnv     string `yaml:"endpoint_env" json:"endpoint_env,omitempty"` // holds https://host[:port]; empty value means AWS
	Bucket          string `yaml:"bucket" json:"bucket,omitempty"`
	Region          string `yaml:"region" json:"region,omitempty"` // us-east-1 when empty; R2 uses auto
	Prefix          string `yaml:"prefix" json:"prefix,omitempty"` // key prefix inside the bucket
	AccessKeyEnv    string `yaml:"access_key_env" json:"access_key_env,omitempty"`
	SecretKeyEnv    string `yaml:"secret_key_env" json:"secret_key_env,omitempty"`
	SessionTokenEnv string `yaml:"session_token_env" json:"session_token_env,omitempty"`
	Addressing      string `yaml:"addressing" json:"addressing,omitempty"` // auto | path | virtual
	// ChecksumSHA256 also sends x-amz-checksum-sha256 on every write, for
	// stores that check it (AWS, MinIO, R2). Content-MD5 and the signed
	// payload hash are always sent.
	ChecksumSHA256 bool `yaml:"checksum_sha256" json:"checksum_sha256,omitempty"`
}

// Enabled reports whether a mirror is configured.
func (c Config) Enabled() bool { return c.MirrorDir != "" || c.S3.Bucket != "" }

// Validate checks the section without touching the network.
func (c Config) Validate() error {
	if c.MirrorDir != "" && c.S3.Bucket != "" {
		return fmt.Errorf("storage: set mirror_dir or s3.bucket, not both")
	}
	switch c.S3.Addressing {
	case "", "auto", "path", "virtual":
	default:
		return fmt.Errorf("storage.s3.addressing must be auto, path or virtual, got %q", c.S3.Addressing)
	}
	if c.Concurrency < 0 {
		return fmt.Errorf("storage.concurrency must not be negative")
	}
	return nil
}

// SweepMinutes returns the sweep interval in minutes, 0 when switched off.
func (c Config) SweepMinutes() int {
	switch {
	case c.SweepIntervalM < 0:
		return 0
	case c.SweepIntervalM == 0:
		return DefaultSweepMinutes
	}
	return c.SweepIntervalM
}

// S3Secrets are the values read from the environment.
type S3Secrets struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	SessionToken string
}

// Secrets reads the endpoint and credentials from the environment variables
// the section names (or the defaults). Missing credentials are an error;
// an empty endpoint means AWS S3.
func (s S3Config) Secrets(lookup func(string) string) (S3Secrets, error) {
	if lookup == nil {
		lookup = os.Getenv
	}
	name := func(v, def string) string {
		if v != "" {
			return v
		}
		return def
	}
	out := S3Secrets{
		Endpoint:  strings.TrimSpace(lookup(name(s.EndpointEnv, DefaultEndpointEnv))),
		AccessKey: strings.TrimSpace(lookup(name(s.AccessKeyEnv, DefaultAccessKeyEnv))),
		SecretKey: strings.TrimSpace(lookup(name(s.SecretKeyEnv, DefaultSecretKeyEnv))),
	}
	if s.SessionTokenEnv != "" {
		out.SessionToken = strings.TrimSpace(lookup(s.SessionTokenEnv))
	}
	if out.AccessKey == "" || out.SecretKey == "" {
		return out, fmt.Errorf("storage.s3: environment variables %s and %s must both be set",
			name(s.AccessKeyEnv, DefaultAccessKeyEnv), name(s.SecretKeyEnv, DefaultSecretKeyEnv))
	}
	return out, nil
}

// RegionOrDefault returns the region, us-east-1 when empty.
func (s S3Config) RegionOrDefault() string {
	if s.Region == "" {
		return DefaultRegion
	}
	return s.Region
}
