package evconfig

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty", Config{}, ""},
		{"folder", Config{MirrorDir: "/m"}, ""},
		{"s3", Config{S3: S3Config{Bucket: "b", Addressing: "virtual"}}, ""},
		{"both", Config{MirrorDir: "/m", S3: S3Config{Bucket: "b"}}, "not both"},
		{"addressing", Config{S3: S3Config{Bucket: "b", Addressing: "dns"}}, "addressing"},
		{"concurrency", Config{Concurrency: -1}, "concurrency"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.cfg.Validate()
			if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Fatalf("Validate: %v, want %q", err, c.want)
			}
		})
	}
	if (Config{}).Enabled() || !(Config{MirrorDir: "m"}).Enabled() || !(Config{S3: S3Config{Bucket: "b"}}).Enabled() {
		t.Fatal("Enabled")
	}
}

func TestSweepMinutes(t *testing.T) {
	for in, want := range map[int]int{0: DefaultSweepMinutes, -1: 0, 5: 5} {
		if got := (Config{SweepIntervalM: in}).SweepMinutes(); got != want {
			t.Errorf("SweepMinutes(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestSecrets(t *testing.T) {
	env := map[string]string{
		DefaultEndpointEnv: " http://minio:9000 ", DefaultAccessKeyEnv: "AK", DefaultSecretKeyEnv: "SK",
		"MY_AK": "ak2", "MY_SK": "sk2", "MY_TOKEN": "tok",
	}
	lookup := func(k string) string { return env[k] }
	s, err := S3Config{}.Secrets(lookup)
	if err != nil || s.Endpoint != "http://minio:9000" || s.AccessKey != "AK" || s.SecretKey != "SK" || s.SessionToken != "" {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	s, err = S3Config{AccessKeyEnv: "MY_AK", SecretKeyEnv: "MY_SK", SessionTokenEnv: "MY_TOKEN"}.Secrets(lookup)
	if err != nil || s.AccessKey != "ak2" || s.SecretKey != "sk2" || s.SessionToken != "tok" {
		t.Fatalf("named: %+v %v", s, err)
	}
	_, err = S3Config{AccessKeyEnv: "NONE"}.Secrets(lookup)
	if err == nil || !strings.Contains(err.Error(), "NONE") || strings.Contains(err.Error(), "SK") && strings.Contains(err.Error(), "=SK") {
		t.Fatalf("missing key: %v", err)
	}
	if (S3Config{}).RegionOrDefault() != DefaultRegion || (S3Config{Region: "auto"}).RegionOrDefault() != "auto" {
		t.Fatal("RegionOrDefault")
	}
}
