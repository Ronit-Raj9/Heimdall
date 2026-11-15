package utils

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Adapter fault protocol bounds apply on both sides of the container boundary.
const (
	AdapterFaultMaxControl    = 64 << 10
	AdapterFaultMaxOperations = 10000
	AdapterFaultMaxLedger     = 64 << 20
)

// ValidateAdapterFault refuses ambiguous rules before resources are allocated.
func ValidateAdapterFault(r schemas.AdapterFaultRule, wallMS int) error {
	if !slices.Contains([]string{"tool", "memory", "browser", "retry"}, r.Kind) {
		return fmt.Errorf("unsupported adapter fault kind %q", r.Kind)
	}
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 256 || len(r.Session) > 256 || len(r.Message) > 4096 {
		return fmt.Errorf("invalid adapter fault name, session or message")
	}
	if !slices.Contains([]string{"error", "timeout", "replace"}, r.Mode) {
		return fmt.Errorf("unsupported adapter fault mode %q", r.Mode)
	}
	if r.FromCall < 0 || r.Calls < 0 {
		return fmt.Errorf("adapter fault call bounds must be non-negative")
	}
	if r.Mode == "timeout" && (r.TimeoutMS <= 0 || (wallMS > 0 && r.TimeoutMS > wallMS)) {
		return fmt.Errorf("adapter fault timeout_ms must be positive and within the run wall budget")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode adapter fault: %w", err)
	}
	if len(raw) > AdapterFaultMaxControl-1024 {
		return fmt.Errorf("adapter fault payload exceeds control message limit")
	}
	return nil
}

// ValidateAdapterFaultHooks checks the exact capabilities advertised by a harness.
func ValidateAdapterFaultHooks(hooks []schemas.AdapterFaultHook, rule schemas.AdapterFaultRule) error {
	if len(hooks) == 0 || len(hooks) > 256 {
		return fmt.Errorf("declare 1..256 adapter fault hooks")
	}
	seen := map[string]bool{}
	found := false
	for _, h := range hooks {
		key := h.Kind + "/" + h.Name
		if seen[key] {
			return fmt.Errorf("duplicate adapter fault hook %s", key)
		}
		seen[key] = true
		if len(h.Modes) == 0 || len(h.Modes) > 3 {
			return fmt.Errorf("invalid fault modes for hook %s", key)
		}
		modes := map[string]bool{}
		for _, mode := range h.Modes {
			if modes[mode] {
				return fmt.Errorf("duplicate mode for hook %s", key)
			}
			modes[mode] = true
			if err := ValidateAdapterFault(schemas.AdapterFaultRule{Kind: h.Kind, Name: h.Name, Mode: mode, TimeoutMS: 1}, 0); err != nil {
				return err
			}
		}
		if h.Kind == rule.Kind && h.Name == rule.Name && slices.Contains(h.Modes, rule.Mode) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("hook %s/%s does not declare mode %s", rule.Kind, rule.Name, rule.Mode)
	}
	return nil
}

// AdapterFaultEntryHash binds every retained row to the previous row.
func AdapterFaultEntryHash(row schemas.AdapterFaultEntry) (string, error) {
	row.Hash = ""
	return HashJSON(row)
}

// VerifyAdapterFaultEvidence detects wrong runs and torn or reordered ledgers.
func VerifyAdapterFaultEvidence(ev *schemas.AdapterFaultEvidence, runID, ruleHash string) error {
	if ev == nil || ev.Version != schemas.AdapterFaultProtocol || ev.RunID != runID || ev.RuleHash != ruleHash {
		return fmt.Errorf("adapter fault evidence identity mismatch")
	}
	prev := ""
	for i, row := range ev.Entries {
		if row.RunID != runID || row.Seq != i+1 || row.PrevHash != prev {
			return fmt.Errorf("adapter fault chain broken at row %d", i+1)
		}
		hash, err := AdapterFaultEntryHash(row)
		if err != nil {
			return err
		}
		if hash != row.Hash {
			return fmt.Errorf("adapter fault row %d hash mismatch", i+1)
		}
		prev = hash
	}
	if prev != ev.LedgerHash {
		return fmt.Errorf("adapter fault ledger hash mismatch")
	}
	return nil
}
