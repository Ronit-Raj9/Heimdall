package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

func TestLiveInitParamsCheck(t *testing.T) {
	ok := []liveInitParams{
		{},
		{BaselineKey: "vk-a", CandidateKey: "vk-b"},
		{TagHeader: "x-bf-lh-agent-version", BaselineTag: "v1", CandidateTag: "v2", URL: "https://gw.example:8080", TokenEnv: "BF_TOKEN"},
	}
	for _, p := range ok {
		if err := p.check(); err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	bad := []liveInitParams{
		{BaselineKey: "vk-a"},
		{BaselineKey: "vk-a", CandidateKey: "vk-a"},
		{BaselineKey: "vk-a", CandidateKey: "vk-b", TagHeader: "x"},
		{TagHeader: "x-h", BaselineTag: "v1"},
		{URL: "file:///etc/passwd"},
		{URL: "http://user:pw@gw"},
		{TokenEnv: "HEIMDALL_MASTER_KEY"},
		{TokenEnv: "AWS_SECRET_ACCESS_KEY"},
		{TokenEnv: "lower-case"},
	}
	for _, p := range bad {
		if err := p.check(); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Fatalf("%+v: want invalid, got %v", p, err)
		}
	}
}

func TestLivePullsRefusedHosted(t *testing.T) {
	env := jobEnv{Hosted: true}
	if _, _, err := validateLivePull(t.Context(), env, nil); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("livescore.pull hosted: %v", err)
	}
	if _, _, err := validateInboxPull(t.Context(), env, nil); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("inbox.pull hosted: %v", err)
	}
}

func TestLiveDecodeRefusesUnknownFields(t *testing.T) {
	var p liveCompareParams
	if err := liveDecode(json.RawMessage(`{"restrat":true}`), &p); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("want invalid, got %v", err)
	}
	if err := (liveCompareParams{Simulate: 5000}).check(); err == nil {
		t.Fatal("simulate cap not enforced")
	}
}
