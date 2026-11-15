package release

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// packTime is the modified time of every file in a pack. A fixed time, a
// fixed file order and fixed modes make the same inputs give the same bytes.
var packTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// packFile is one file of an evidence pack.
type packFile struct {
	name string
	data []byte
}

// packFiles collects the files of a release's evidence pack, in pack order:
// the readme, the record, the manifest, the analysis, the seal chain, then
// every counted run in run id order. Run files are copied byte for byte so
// their hashes match seal.json and seals.log. rec must not carry
// evidence_sha256 or hash yet: release.json is the record before the pack
// was hashed.
func packFiles(runsRoot string, rec *schemas.ReleaseRecord, ev *evidence) ([]packFile, error) {
	expDir := filepath.Join(runsRoot, rec.ExpID)
	recJSON, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release.json: %w", err)
	}
	runs := append([]schemas.SealedRun(nil), ev.runs...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].Run.RunID < runs[j].Run.RunID })
	files := []packFile{
		{"README.txt", []byte(readme(rec, len(runs), ev.policyJSON != nil))},
		{"release.json", append(recJSON, '\n')},
	}
	usedNames := map[string]bool{"README.txt": true, "release.json": true}
	add := func(name, src string, required bool) error {
		b, err := os.ReadFile(src)
		if errors.Is(err, fs.ErrNotExist) && !required {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if usedNames[name] {
			return fmt.Errorf("duplicate pack file %s", name)
		}
		usedNames[name] = true
		files = append(files, packFile{name, b})
		return nil
	}
	if err := add("manifest.json", filepath.Join(expDir, manifestFile), true); err != nil {
		return nil, err
	}
	if err := add("analysis/verdict.json", filepath.Join(ev.analysisDir, verdictFile), true); err != nil {
		return nil, err
	}
	if err := add("analysis/report.md", filepath.Join(ev.analysisDir, reportFile), false); err != nil {
		return nil, err
	}
	if ev.policyJSON != nil {
		files = append(files, packFile{"analysis/policy.json", ev.policyJSON})
	}
	if err := add("seals.log", filepath.Join(expDir, sealsLogFile), true); err != nil {
		return nil, err
	}
	for _, run := range runs {
		id := run.Run.RunID
		sealPath := filepath.Join(run.Dir, "seal.json")
		sealBytes, err := os.ReadFile(sealPath)
		if err != nil {
			return nil, fmt.Errorf("read runs/%s/seal.json: %w", id, err)
		}
		if run.SealHash != "" {
			got := utils.SHA256Bytes(sealBytes)
			want := strings.TrimPrefix(run.SealHash, "sha256:")
			if got != want {
				return nil, fmt.Errorf("runs/%s/seal.json hashes to %s, run records %s: %w", id, got, run.SealHash, schemas.ErrSealMismatch)
			}
		}
		sealName := "runs/" + id + "/seal.json"
		if usedNames[sealName] {
			return nil, fmt.Errorf("duplicate pack file %s", sealName)
		}
		usedNames[sealName] = true
		files = append(files, packFile{sealName, sealBytes})
		var seal schemas.SealRecord
		if err := json.Unmarshal(sealBytes, &seal); err != nil {
			return nil, fmt.Errorf("parse runs/%s/seal.json: %w", id, err)
		}
		if seal.RunID != "" && seal.RunID != id {
			return nil, fmt.Errorf("runs/%s/seal.json names run %q", id, seal.RunID)
		}
		paths := make([]string, 0, len(seal.Files))
		for name := range seal.Files {
			if err := validateSealedRunPath(name); err != nil {
				return nil, fmt.Errorf("runs/%s/seal.json file %q: %w", id, name, err)
			}
			paths = append(paths, name)
		}
		sort.Strings(paths)
		for _, name := range paths {
			packName := "runs/" + id + "/" + name
			data, err := os.ReadFile(filepath.Join(run.Dir, filepath.FromSlash(name)))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", packName, err)
			}
			if got, want := utils.SHA256Bytes(data), seal.Files[name]; got != want {
				return nil, fmt.Errorf("%s hashes to %s, seal lists %s: %w", packName, got, want, schemas.ErrSealMismatch)
			}
			if usedNames[packName] {
				return nil, fmt.Errorf("duplicate pack file %s", packName)
			}
			usedNames[packName] = true
			files = append(files, packFile{packName, data})
		}
		if run.GradeSource != "" && run.GradeSource != schemas.GradeSourceOriginal {
			src := filepath.Join(expDir, "regrades", run.GradeSource, "runs", id, "grade.json")
			if err := add("runs/"+id+"/regrade.grade.json", src, false); err != nil {
				return nil, err
			}
			src = filepath.Join(expDir, "regrades", run.GradeSource, "runs", id, "regate.json")
			if err := add("runs/"+id+"/regrade.regate.json", src, false); err != nil {
				return nil, err
			}
		}
		if run.Review != nil { // human review the outcome came from (docs/human-review.md)
			if err := add("runs/"+id+"/review.json", filepath.Join(expDir, "reviews", run.Review.ReviewID, "review.json"), true); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

// validateSealedRunPath accepts only a relative path inside a run folder. A
// seal is persisted input, so pack construction validates it before using it
// as either a filesystem path or an archive entry name.
func validateSealedRunPath(name string) error {
	if name == "" || strings.ContainsRune(name, '\x00') || strings.Contains(name, "\\") {
		return errors.New("invalid relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("path escapes run folder")
	}
	if filepath.ToSlash(clean) != name {
		return errors.New("path is not canonical")
	}
	return nil
}

// buildPack zips files in the given order with fixed times and modes.
// compress/flate is deterministic for the same input and Go version.
func buildPack(files []packFile) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		fh := &zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: packTime}
		fh.SetMode(0o444)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			return nil, fmt.Errorf("zip %s: %w", f.name, err)
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, fmt.Errorf("zip %s: %w", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("close zip: %w", err)
	}
	return buf.Bytes(), nil
}

// readme says what each file is and how to check it by hand.
func readme(rec *schemas.ReleaseRecord, nRuns int, hasPolicy bool) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	policy := "no policy was evaluated"
	if hasPolicy {
		policy = fmt.Sprintf("policy %s decided %s", rec.PolicyName, rec.PolicyDecision)
	}
	override := "none"
	if rec.Override != "" {
		override = rec.Override
	}
	line("Bifrost Bench evidence pack")
	line("")
	line("Release %s of experiment %s, analysis %s.", rec.ReleaseID, rec.ExpID, rec.AnalysisID)
	line("Arm %s, agent config %s, config hash %s.", rec.Arm, rec.AgentConfigID, rec.ConfigHash)
	line("Verdict %s, action %s; %s.", rec.Decision, rec.Action, policy)
	line("Approved by %s at %s. Override: %s.", rec.Approver, rec.CreatedAt.Format(time.RFC3339), override)
	line("Counted sealed runs: %d.", nRuns)
	line("")
	line("This pack records a release decision and the evidence behind it. The")
	line("release log is hash-chained, not signed: the chain shows that a record")
	line("has not changed since it was written, not who wrote it. It is not a")
	line("cryptographic signature and not a compliance certification.")
	line("")
	line("Files")
	line("  README.txt               this file")
	line("  release.json             the release record as it was before this pack was")
	line("                           hashed: evidence_sha256 and hash are empty here; the")
	line("                           record's line in releases.jsonl carries both")
	line("  manifest.json            the frozen experiment manifest (manifest_hash)")
	line("  analysis/verdict.json    the analysis the release cites (analysis_id)")
	line("  analysis/report.md       that analysis's markdown report, when written")
	line("  analysis/policy.json     the policy result, when a policy was evaluated")
	line("  seals.log                the experiment's seal chain: \"<run_id> <seal hash>\"")
	line("  runs/<run_id>/seal.json  each counted run's seal: every file of the sealed")
	line("                           run folder with its sha256")
	line("  runs/<run_id>/<every file named by seal.json>")
	line("                           byte-for-byte copies of every sealed run file,")
	line("                           including raw agent streams, gateway requests,")
	line("                           tool ledgers, events, and cost rows when present")
	line("  runs/<run_id>/regrade.grade.json")
	line("                           the regrade grade, when the analysis used a regrade")
	line("  runs/<run_id>/regrade.regate.json")
	line("                           the gates recomputed after the fact from the sealed")
	line("                           evidence, when the analysis used a regate; they")
	line("                           replace gates.json, the outcome and the metrics")
	line("                           listed in it")
	line("")
	line("How to verify")
	line("  1. Seals. For each run, sha256sum runs/<run_id>/seal.json. The hash must be")
	line("     on that run's line in seals.log, and every copied run file must hash to")
	line("     its value under \"files\" in that seal.json.")
	line("  2. Seal chain. In seals.log, each run's seal.json has prev_seal equal to the")
	line("     hash on the line before; the first line's prev_seal is empty.")
	line("  3. Seals digest. Sort the seal hashes of runs/*/seal.json, write each")
	line("     followed by a newline, and sha256 the result. It must equal seals_digest")
	line("     in release.json.")
	line("  4. Release chain. Run `bifrost-bench release verify`. By hand: in")
	line("     releases.jsonl, each line's prev_hash equals the hash of the line")
	line("     before, and hash is the sha256 of the line's canonical JSON with hash")
	line("     set to \"\". This release's line carries evidence_sha256, the sha256 of")
	line("     this zip file.")
	return b.String()
}
