package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fix wave 3, SEC-B: on the hosted server and the shared worker skills.extra
// may only name an agent's upload collection, where every skill is gated by
// its scan; local servers keep the old freedom (D34).

func TestHostedSkillsExtraOutsideUploadRefused(t *testing.T) {
	RestrictSkillsExtra(true)
	defer RestrictSkillsExtra(false)
	root := t.TempDir()
	gateWrite(t, root, map[string]string{
		"tools/x/SKILL.md":                     "# x\n",
		"agents/zz/myskills/a/SKILL.md":        "# a\n",
		"agents/zz/skills/good/SKILL.md":       "# good\n",
		"agents/zz/skills/good/inner/SKILL.md": "# inner\n",
		"agents/zz/skills-pending/p/SKILL.md":  "# p\n",
		"agents/yy/README.md":                  "x\n",
	})
	gateRecord(t, root, "zz", "good", "admitted", gateTreeHash(t, filepath.Join(root, "agents/zz/skills/good")))
	if err := os.Symlink(filepath.Join(root, "tools"), filepath.Join(root, "agents/yy/skills")); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{"tools/x", "tools", "agents/zz/myskills", "agents/zz/skills-pending", "agents/zz/skills-pending/p", "agents/zz/skills/good/inner", "agents/yy/skills", "agents/yy/skills/x", "agents/zz"} {
		_, err := ResolveSkills(root, gateCfg(extra), nil)
		if err == nil || !strings.Contains(err.Error(), SkillsExtraRefusal) {
			t.Errorf("skills.extra %q: err %v, want the refusal", extra, err)
		}
		if err := CheckSkillsExtra(gateCfg(extra)); extra != "agents/yy/skills" && extra != "agents/yy/skills/x" && (err == nil || err.Error() != SkillsExtraRefusal) {
			t.Errorf("CheckSkillsExtra(%q) = %v, want the refusal", extra, err)
		}
	}
	for _, extra := range []string{"agents/zz/skills", "./agents/zz/skills/", "agents/zz/skills/good"} {
		got, err := ResolveSkills(root, gateCfg(extra), nil)
		if err != nil || len(got) != 1 || got[0].Name != "good" {
			t.Errorf("skills.extra %q: %+v, %v", extra, got, err)
		}
		if err := CheckSkillsExtra(gateCfg(extra)); err != nil {
			t.Errorf("CheckSkillsExtra(%q) = %v", extra, err)
		}
	}
	if err := CheckSkillsExtra(nil); err != nil {
		t.Errorf("CheckSkillsExtra(nil) = %v", err)
	}
}

func TestLocalSkillsExtraAnywhereAllowed(t *testing.T) {
	RestrictSkillsExtra(false)
	root := t.TempDir()
	gateWrite(t, root, map[string]string{"tools/x/SKILL.md": "# x\n"})
	got, err := ResolveSkills(root, gateCfg("tools/x"), nil)
	if err != nil || len(got) != 1 || got[0].Name != "x" {
		t.Fatalf("local skills.extra: %+v, %v", got, err)
	}
	if err := CheckSkillsExtra(gateCfg("tools/x")); err != nil {
		t.Fatalf("local CheckSkillsExtra: %v", err)
	}
}

// TestSkillGateSentenceIsStable pins the two refusal sentences of the skill
// gate: the evaluation page reads the agent and the skill out of them to
// offer "Scan it now" (fix wave 3, L5, D31). Change them only together with
// that page.
func TestSkillGateSentenceIsStable(t *testing.T) {
	root := t.TempDir()
	gateWrite(t, root, map[string]string{"agents/codex/skills/review/SKILL.md": "# review\n"})
	s := Skill{Name: "review", Host: filepath.Join(root, "agents/codex/skills/review")}
	err := gateUploadedSkill(root, "codex", s)
	if want := "skill review in agents/codex/skills has no passing scan: open the agent's Tools tab and press Scan"; err == nil || err.Error() != want {
		t.Fatalf("no scan: %v, want %q", err, want)
	}
	gateRecord(t, root, "codex", "review", "admitted", gateTreeHash(t, s.Host))
	gateWrite(t, root, map[string]string{"agents/codex/skills/review/SKILL.md": "# review v2\n"})
	err = gateUploadedSkill(root, "codex", s)
	if want := "skill review in agents/codex/skills changed after its scan: open the agent's Tools tab and press Scan"; err == nil || err.Error() != want {
		t.Fatalf("changed: %v, want %q", err, want)
	}
	if want := "skills.extra may only name an agent's uploaded skills (agents/<id>/skills) on this server. Upload the skill on the agent's Tools tab."; SkillsExtraRefusal != want {
		t.Fatalf("SkillsExtraRefusal = %q, want %q", SkillsExtraRefusal, want)
	}
}
