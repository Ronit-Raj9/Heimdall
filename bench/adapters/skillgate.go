package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// Skill scan gate (fix wave 2, N22). A skill uploaded to an agent lands in
// agents/<id>/skills-pending/<name>/, is scanned, and moves to
// agents/<id>/skills/<name>/ (the upload collection that skills.extra lists)
// only when the scan admits it. The scan's outcome is kept in
// agents/<id>/skill-scans/<name>.json. ResolveSkills refuses a skill of the
// upload collection whose record is missing, not admitting, or older than
// the folder's current files, so a skill written there by any other path
// (or installed before the gate) cannot run until it passes a scan.

// Folder names under agents/<id>/ the skill gate uses.
const (
	SkillUploadDir  = "skills"
	SkillPendingDir = "skills-pending"
	SkillScansDir   = "skill-scans"
)

// SkillScanFile is agents/<id>/skill-scans/<name>.json.
type SkillScanFile struct {
	Decision   string                   `json:"decision"`    // admitted | blocked
	TreeSHA256 string                   `json:"tree_sha256"` // SkillTreeSHA256 of the folder that was scanned
	ScannedAt  time.Time                `json:"scanned_at"`
	Record     *schemas.AdmitScanRecord `json:"record"`
}

// SkillScanPath is the project-relative path of an agent skill's scan file.
func SkillScanPath(agent, name string) string {
	return "agents/" + agent + "/" + SkillScansDir + "/" + name + ".json"
}

// SkillTreeSHA256 hashes a skill folder: sha256 over the sorted lines
// "<slash path relative to dir>\x00<sha256 of the file>\n" of every regular
// file. Symlinks and other special files are refused, as the scan refuses
// them.
func SkillTreeSHA256(dir string) (string, error) {
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", filepath.ToSlash(rel))
		}
		sum, err := utils.SHA256File(p)
		if err != nil {
			return err
		}
		lines = append(lines, filepath.ToSlash(rel)+"\x00"+sum+"\n")
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash skill folder: %w", err)
	}
	sort.Strings(lines)
	h := sha256.Sum256([]byte(strings.Join(lines, "")))
	return hex.EncodeToString(h[:]), nil
}

// uploadCollection reports whether dir (resolved, inside realRoot) is an
// agent's upload collection agents/<id>/skills, or a skill folder inside it
// or inside agents/<id>/skills-pending. It returns the agent id, and the
// skill name when dir is one skill folder.
func uploadCollection(realRoot, dir string) (agent, skill string, ok bool) {
	rel, err := filepath.Rel(realRoot, dir)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 3 || parts[0] != "agents" {
		return "", "", false
	}
	switch {
	case len(parts) == 3 && parts[2] == SkillUploadDir:
		return parts[1], "", true
	case len(parts) == 3 && parts[2] == SkillPendingDir:
		return parts[1], "", true
	case len(parts) == 4 && (parts[2] == SkillUploadDir || parts[2] == SkillPendingDir):
		return parts[1], parts[3], true
	}
	return "", "", false
}

// gateUploadedSkill checks that skill s of agent's upload collection has a
// passing scan of its current files.
func gateUploadedSkill(realRoot, agent string, s Skill) error {
	where := "agents/" + agent + "/" + SkillUploadDir
	noScan := fmt.Errorf("skill %s in %s has no passing scan: open the agent's Tools tab and press Scan", s.Name, where)
	data, err := os.ReadFile(filepath.Join(realRoot, filepath.FromSlash(SkillScanPath(agent, s.Name))))
	if err != nil {
		return noScan
	}
	var rec SkillScanFile
	if json.Unmarshal(data, &rec) != nil || rec.Decision != schemas.AdmitScanAdmitted || rec.TreeSHA256 == "" {
		return noScan
	}
	sum, err := SkillTreeSHA256(s.Host)
	if err != nil {
		return fmt.Errorf("skill %s in %s: %w", s.Name, where, err)
	}
	if sum != rec.TreeSHA256 {
		return fmt.Errorf("skill %s in %s changed after its scan: open the agent's Tools tab and press Scan", s.Name, where)
	}
	return nil
}

// emptyUploadCollection reports whether rel names an agent's upload
// collection that is missing or holds no visible entry: every skill was
// uninstalled (blocked) or deleted, so the agent installs none from it.
func emptyUploadCollection(root, rel string) bool {
	c := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	parts := strings.Split(c, "/")
	if len(parts) != 3 || parts[0] != "agents" || parts[2] != SkillUploadDir || filepath.IsAbs(rel) {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(c)))
	if os.IsNotExist(err) {
		return true
	}
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false
		}
	}
	return true
}
