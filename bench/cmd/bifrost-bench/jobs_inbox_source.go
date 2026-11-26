package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"gopkg.in/yaml.v3"
)

// Gap lane G03: inbox.source adds one pull source to inbox/sources.yaml from
// the Inbox page form, so a first-time user never edits YAML to pull. The
// credentials are named, never given: an org secret on the hosted server, a
// server variable locally. Comments and other sources in the file are kept.

const jobInboxSource = "inbox.source"

func init() {
	registerJobKind(jobKindSpec{Kind: jobInboxSource, Local: true, Hosted: true, Validate: validateInboxSource, Run: runInboxSource})
}

// inboxSourceParams are the params of inbox.source.
type inboxSourceParams struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"` // bifrost, langfuse, langsmith
	URL          string `json:"url,omitempty"`
	Host         string `json:"host,omitempty"`
	TokenEnv     string `json:"token_env,omitempty"`
	PublicKeyEnv string `json:"public_key_env,omitempty"`
	SecretKeyEnv string `json:"secret_key_env,omitempty"`
	APIKeyEnv    string `json:"api_key_env,omitempty"`
	Project      string `json:"project,omitempty"`
	// Remove deletes the source named Name instead of adding one (wave 4,
	// p13-2). Its items stay in the inbox.
	Remove bool `json:"remove,omitempty"`
}

// source turns the params into a Source with the starter filter of its
// kind (errors for Bifrost and LangSmith, level ERROR for Langfuse).
func (p inboxSourceParams) source() traceinbox.Source {
	s := traceinbox.Source{Name: p.Name, Kind: p.Kind}
	switch p.Kind {
	case traceinbox.KindBifrost:
		s.URL, s.TokenEnv = p.URL, p.TokenEnv
		s.Filters.Errors = true
	case traceinbox.KindLangfuse:
		s.Host, s.PublicKeyEnv, s.SecretKeyEnv, s.Project = p.Host, p.PublicKeyEnv, p.SecretKeyEnv, p.Project
		s.Filters.LevelError = true
	case traceinbox.KindLangSmith:
		s.Host, s.APIKeyEnv, s.Project = p.Host, p.APIKeyEnv, p.Project
		s.Filters.Errors = true
	}
	return s
}

func inboxSourcesPath(env jobEnv) string {
	return filepath.Join(env.App.cfg.Path(traceinbox.Dir), traceinbox.SourcesFile)
}

// inboxSourceYAML returns sources.yaml with the new source appended, after
// checking every rule a pull would.
func inboxSourceYAML(env jobEnv, p inboxSourceParams) ([]byte, error) {
	if p.Remove {
		return inboxSourceRemoveYAML(env, p.Name)
	}
	if !hostedPullable(p.Kind) {
		return nil, liveInvalid("kind must be bifrost, langfuse or langsmith")
	}
	if !liveIdentRE.MatchString(p.Name) {
		return nil, liveInvalid("name is required (letters, digits, . _ - only)")
	}
	for _, e := range []string{p.TokenEnv, p.PublicKeyEnv, p.SecretKeyEnv, p.APIKeyEnv} {
		if e != "" && (!liveEnvNameRE.MatchString(e) || liveReservedEnv(e)) {
			return nil, liveInvalid("%s is not a secret name like LANGSMITH_API_KEY", e)
		}
	}
	src := p.source()
	for _, u := range []string{src.URL, src.Host} {
		if u == "" {
			continue
		}
		if env.Hosted {
			if err := handlers.CheckPullURL(u); err != nil {
				return nil, liveInvalid("%v", err)
			}
		}
	}
	if err := src.Validate(); err != nil {
		return nil, liveInvalid("%v", err)
	}
	path := inboxSourcesPath(env)
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc, seq, err := inboxSourcesDoc(data)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := n.Encode(src); err != nil {
		return nil, err
	}
	seq.Content = append(seq.Content, &n)
	return inboxSourcesEncode(&doc)
}

// inboxSourcesDoc parses sources.yaml as a node tree (comments kept) and
// returns it with its sources list, created when missing.
func inboxSourcesDoc(data []byte) (yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return doc, nil, liveInvalid("inbox/sources.yaml does not parse: %v", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return doc, nil, liveInvalid("inbox/sources.yaml is not a mapping")
	}
	var seq *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "sources" {
			seq = root.Content[i+1]
		}
	}
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "sources"}, seq)
	}
	if seq.Kind != yaml.SequenceNode {
		if seq.Kind == yaml.ScalarNode && (seq.Value == "" || seq.Tag == "!!null") {
			*seq = yaml.Node{Kind: yaml.SequenceNode}
		} else {
			return doc, nil, liveInvalid("sources in inbox/sources.yaml is not a list")
		}
	}
	return doc, seq, nil
}

// inboxSourcesEncode writes the node tree back and checks that it loads
// exactly as a pull would load it.
func inboxSourcesEncode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	// The result must load exactly as a pull would load it.
	tmp, err := os.CreateTemp("", "sources-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	if _, err := traceinbox.LoadConfig(tmp.Name()); err != nil {
		return nil, liveInvalid("%v", err)
	}
	return buf.Bytes(), nil
}

// inboxSourceRemoveYAML returns sources.yaml without the source named name.
func inboxSourceRemoveYAML(env jobEnv, name string) ([]byte, error) {
	if !liveIdentRE.MatchString(name) {
		return nil, liveInvalid("name is required (letters, digits, . _ - only)")
	}
	data, err := os.ReadFile(inboxSourcesPath(env))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	doc, seq, err := inboxSourcesDoc(data)
	if err != nil {
		return nil, err
	}
	at := -1
	for i, n := range seq.Content {
		if n.Kind != yaml.MappingNode {
			continue
		}
		for k := 0; k+1 < len(n.Content); k += 2 {
			if n.Content[k].Value == "name" && n.Content[k+1].Value == name {
				at = i
			}
		}
	}
	if at < 0 {
		return nil, liveInvalid("no source named %s in inbox/sources.yaml", name)
	}
	seq.Content = append(seq.Content[:at], seq.Content[at+1:]...)
	return inboxSourcesEncode(&doc)
}

func validateInboxSource(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p inboxSourceParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if _, err := inboxSourceYAML(env, p); err != nil {
		if errors.Is(err, jobs.ErrInvalidParams) {
			return nil, "", err
		}
		return nil, "", liveInvalid("%v", err)
	}
	return p, "inbox/sources.yaml", nil
}

func runInboxSource(_ context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p inboxSourceParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	data, err := inboxSourceYAML(env, p)
	if err != nil {
		return nil, err
	}
	path := inboxSourcesPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if p.Remove {
		log.Printf("removed source %s", p.Name)
		return map[string]string{"source": p.Name, "path": "inbox/sources.yaml", "removed": "true"}, nil
	}
	next := "store the credentials it names, then pull"
	if env.Hosted {
		next = "add the secrets it names in Settings > Secrets, then pull"
	}
	log.Printf("added source %s (%s) to inbox/sources.yaml", p.Name, p.Kind)
	log.Printf("next: %s", next)
	return map[string]string{"source": p.Name, "path": "inbox/sources.yaml", "next": next}, nil
}
