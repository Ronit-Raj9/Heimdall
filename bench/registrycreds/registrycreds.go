// Package registrycreds keeps an organization's private image registry
// logins (lane Y10): Docker Hub, GitHub (GHCR), Amazon ECR, Google (GCR and
// Artifact Registry), Azure (ACR) and any other registry with a user name
// and password. A login is stored in the keystore as kind "registry" and
// names a Settings > Secrets entry that holds the password, token, secret
// access key or service account JSON; the login itself holds no secret.
//
// The server resolves the logins for each lease (an ECR login becomes a
// short-lived token from GetAuthorizationToken, so the access key never
// leaves the server) and hands them to the runner, which uses one for a
// single `docker pull` through a temporary Docker config (Pull).
package registrycreds

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/maximhq/bifrost/bench/utils"
)

// Registry kinds.
const (
	KindDockerHub = "dockerhub"
	KindGHCR      = "ghcr"
	KindECR       = "ecr"
	KindGCR       = "gcr" // Container Registry and Artifact Registry
	KindACR       = "acr"
	KindOther     = "other"
)

// Kinds lists the registry kinds in display order.
var Kinds = []string{KindDockerHub, KindGHCR, KindECR, KindGCR, KindACR, KindOther}

// GCRUsername is the user name of a Google service account JSON key login.
const GCRUsername = "_json_key"

// MaxPerOrg caps the registry logins of one org.
const MaxPerOrg = 20

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("registry login")

var (
	ecrHostRe   = regexp.MustCompile(`^[0-9]{12}\.dkr\.ecr(-fips)?\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)
	gcrHostRe   = regexp.MustCompile(`^([a-z]+\.)?gcr\.io$|^[a-z0-9-]+-docker\.pkg\.dev$`)
	acrHostRe   = regexp.MustCompile(`^[a-z0-9]{5,50}\.azurecr\.(io|cn|us)$`)
	hostRe      = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
	regionRe    = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
	accessKeyRe = regexp.MustCompile(`^[A-Z0-9]{16,128}$`)
)

// Settings are the stored fields of a login. None of them is secret.
type Settings struct {
	Username   string `json:"username"`
	SecretName string `json:"secret_name"`
	Region     string `json:"region,omitempty"` // ECR only
}

// Login is one registry login.
type Login struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Host       string    `json:"host"`
	Username   string    `json:"username"`
	SecretName string    `json:"secret_name"`
	Region     string    `json:"region,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Input is a new login as a user enters it.
type Input struct {
	Kind       string `json:"kind"`
	Host       string `json:"host,omitempty"`
	Username   string `json:"username,omitempty"`
	SecretName string `json:"secret_name"`
	Region     string `json:"region,omitempty"`
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Normalize validates in and fills what the kind implies (the Docker Hub
// and GHCR hosts, the ECR region from its host, the Google user name).
func Normalize(in Input) (Input, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	in.Host = schemas.NormalizeRegistryHost(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.SecretName = strings.TrimSpace(in.SecretName)
	in.Region = strings.ToLower(strings.TrimSpace(in.Region))
	switch in.Kind {
	case KindDockerHub:
		if in.Host == "" {
			in.Host = schemas.DockerHubHost
		}
		if in.Host != schemas.DockerHubHost {
			return in, invalid("a Docker Hub login is for docker.io")
		}
	case KindGHCR:
		if in.Host == "" {
			in.Host = "ghcr.io"
		}
		if in.Host != "ghcr.io" {
			return in, invalid("a GitHub login is for ghcr.io")
		}
	case KindECR:
		m := ecrHostRe.FindStringSubmatch(in.Host)
		if m == nil {
			return in, invalid("an ECR host looks like 123456789012.dkr.ecr.us-east-1.amazonaws.com")
		}
		if in.Region == "" {
			in.Region = m[2]
		}
		if in.Region != m[2] {
			return in, invalid("region %s does not match the host's region %s", in.Region, m[2])
		}
		if !accessKeyRe.MatchString(in.Username) {
			return in, invalid("for ECR the user name is the AWS access key id (AKIA...); the secret holds the secret access key")
		}
	case KindGCR:
		if !gcrHostRe.MatchString(in.Host) {
			return in, invalid("a Google host is gcr.io, us.gcr.io or <region>-docker.pkg.dev")
		}
		in.Username = GCRUsername
	case KindACR:
		if !acrHostRe.MatchString(in.Host) {
			return in, invalid("an Azure host looks like myregistry.azurecr.io")
		}
	case KindOther:
		if in.Host == "" || !hostRe.MatchString(in.Host) {
			return in, invalid("host must be a registry host name such as registry.example.com or registry.example.com:5000")
		}
	default:
		return in, invalid("kind must be one of %s", strings.Join(Kinds, ", "))
	}
	if in.Kind != KindECR {
		in.Region = ""
	}
	if in.Username == "" || len(in.Username) > 256 || strings.ContainsAny(in.Username, ":\r\n\t ") {
		return in, invalid("user name is required (no spaces or colons)")
	}
	if err := toolproxy.CheckOrgSecretName(in.SecretName); err != nil {
		return in, invalid("%s; store the password or token under Settings > Secrets first and name it here", err.Error())
	}
	return in, nil
}

// KeySource is the slice of keystore.Store this package reads.
type KeySource interface {
	List(ctx context.Context, orgID string, k keystore.Kind) ([]keystore.Key, error)
	Reveal(ctx context.Context, orgID, id string) (string, error)
}

// FromKey decodes a stored login. Reveal reads its settings.
func FromKey(ctx context.Context, ks KeySource, k keystore.Key) (Login, error) {
	raw, err := ks.Reveal(ctx, k.OrgID, k.ID)
	if err != nil {
		return Login{}, err
	}
	var s Settings
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Login{}, fmt.Errorf("registry login %s: stored settings unreadable", k.ID)
	}
	return Login{ID: k.ID, Kind: k.Provider, Host: k.Label, Username: s.Username, SecretName: s.SecretName,
		Region: s.Region, CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt}, nil
}

// Put is the keystore record of a normalized input.
func Put(in Input) (keystore.Put, error) {
	b, err := json.Marshal(Settings{Username: in.Username, SecretName: in.SecretName, Region: in.Region})
	if err != nil {
		return keystore.Put{}, err
	}
	return keystore.Put{Provider: in.Kind, Label: in.Host, Value: string(b)}, nil
}

// List returns an org's logins, sorted by host.
func List(ctx context.Context, ks KeySource, orgID string) ([]Login, error) {
	keys, err := ks.List(ctx, orgID, keystore.KindRegistry)
	if err != nil {
		return nil, err
	}
	out := make([]Login, 0, len(keys))
	for _, k := range keys {
		l, err := FromKey(ctx, ks, k)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out, nil
}

// secretValues reads the org's secrets by name.
func secretValues(ctx context.Context, ks KeySource, orgID string) (map[string]string, error) {
	keys, err := ks.List(ctx, orgID, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		if strings.HasPrefix(k.CreatedBy, "heimdall:") {
			continue // the server's own credentials are never a registry password
		}
		v, err := ks.Reveal(ctx, orgID, k.ID)
		if err != nil {
			return nil, err
		}
		out[k.Label] = v
	}
	return out, nil
}

// SecretNames returns the names of the org's secrets (no values).
func SecretNames(ctx context.Context, ks KeySource, orgID string) (map[string]bool, error) {
	keys, err := ks.List(ctx, orgID, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, k := range keys {
		if !strings.HasPrefix(k.CreatedBy, "heimdall:") {
			out[k.Label] = true
		}
	}
	return out, nil
}

// Resolver turns logins into registry auths. The zero value is ready.
type Resolver struct {
	ECR ECRClient // zero value calls AWS
}

// Problem is a login that could not be resolved, without any secret.
type Problem struct {
	Host   string `json:"host"`
	Reason string `json:"reason"`
}

// Resolve returns the org's registry auths for a lease, and the logins
// that could not be resolved (a missing secret, an ECR refusal). Errors
// reading the store are returned as err.
func (r *Resolver) Resolve(ctx context.Context, ks KeySource, orgID string) ([]schemas.RegistryAuth, []Problem, error) {
	logins, err := List(ctx, ks, orgID)
	if err != nil || len(logins) == 0 {
		return nil, nil, err
	}
	secrets, err := secretValues(ctx, ks, orgID)
	if err != nil {
		return nil, nil, err
	}
	var out []schemas.RegistryAuth
	var problems []Problem
	for _, l := range logins {
		secret, ok := secrets[l.SecretName]
		if !ok || secret == "" {
			problems = append(problems, Problem{Host: l.Host, Reason: "secret " + l.SecretName + " is missing under Settings > Secrets"})
			continue
		}
		if l.Kind == KindECR {
			a, err := r.ECR.Token(ctx, l.Host, l.Region, l.Username, secret)
			if err != nil {
				problems = append(problems, Problem{Host: l.Host, Reason: err.Error()})
				continue
			}
			out = append(out, a)
			continue
		}
		out = append(out, schemas.RegistryAuth{Host: l.Host, Username: l.Username, Password: secret})
	}
	return out, problems, nil
}

// For returns the auth of the image's registry host, if any.
func For(ref string, auths []schemas.RegistryAuth) (schemas.RegistryAuth, bool) {
	host := schemas.ImageRegistryHost(ref)
	for _, a := range auths {
		if schemas.NormalizeRegistryHost(a.Host) == host {
			return a, true
		}
	}
	return schemas.RegistryAuth{}, false
}

// DockerConfig is the config.json of one registry auth.
func DockerConfig(a schemas.RegistryAuth) []byte {
	key := schemas.NormalizeRegistryHost(a.Host)
	if key == schemas.DockerHubHost {
		key = "https://index.docker.io/v1/"
	}
	auth := base64.StdEncoding.EncodeToString([]byte(a.Username + ":" + a.Password))
	b, _ := json.Marshal(map[string]any{"auths": map[string]any{key: map[string]string{"auth": auth}}})
	return b
}

// Puller runs the docker CLI (utils.Docker under the org's policy).
type Puller interface {
	MustRunEnv(ctx context.Context, env []string, args ...string) (*utils.DockerResult, error)
}

// Pull pulls ref with the auth of its registry host: it writes a Docker
// config to a fresh private folder, runs `docker pull` with DOCKER_CONFIG
// pointing at it, and removes the folder. The caller's Docker policy still
// decides whether the image may be pulled at all. No matching auth is an
// error.
func Pull(ctx context.Context, d Puller, ref string, auths []schemas.RegistryAuth) error {
	a, ok := For(ref, auths)
	if !ok {
		return fmt.Errorf("no registry login for %s", schemas.ImageRegistryHost(ref))
	}
	dir, err := os.MkdirTemp("", "heimdall-pull-")
	if err != nil {
		return fmt.Errorf("pull %s: temporary Docker config: %w", ref, err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("pull %s: temporary Docker config: %w", ref, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), DockerConfig(a), 0o600); err != nil {
		return fmt.Errorf("pull %s: temporary Docker config: %w", ref, err)
	}
	if _, err := d.MustRunEnv(ctx, []string{"DOCKER_CONFIG=" + dir}, "pull", ref); err != nil {
		return redact(err, a)
	}
	return nil
}

// redact keeps a password out of an error, should docker ever echo it.
func redact(err error, a schemas.RegistryAuth) error {
	msg := err.Error()
	if a.Password != "" && strings.Contains(msg, a.Password) {
		msg = strings.ReplaceAll(msg, a.Password, "[hidden]")
	}
	return errors.New(msg)
}
