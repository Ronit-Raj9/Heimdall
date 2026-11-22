package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/imagepin"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Lock fingerprints (docs/lock-fingerprints.md) close the gap the legacy
// lock fields leave: those hash selected files, so a changed verifier
// command, timeout, protected path, probe list, grader version or image
// could run under an old admission record. The fingerprints section binds
// the whole parsed suite.yaml, task.yaml and probe YAML, every file they
// name, the grader and environment part versions and the image identities.
// It is additive and versioned: a lock without it is a legacy lock, encodes
// exactly as before, and keeps every manifest and seal that points at it
// verifying.

// LegacyLockWarning is the one line printed for a lock without fingerprints.
const LegacyLockWarning = "legacy lock: evaluator settings are not bound; run `suite lock` to add fingerprints"

// Fingerprint parts (schemas.FingerprintDrift.Part).
const (
	PartSettings = "settings"
	PartFiles    = "files"
	PartImage    = "image"
)

// Image roles in a fingerprint's Images map. Services use "service/<id>".
const (
	ImageBase      = "base"
	ImageHelper    = "helper"
	ImageToolProxy = "toolproxy"
	ImageTask      = "task"
)

// Image identity forms in a fingerprint's Images map:
//
//	tag only: <tag>              bound by tag (the default for a locally built image)
//	<tag>=<repo>@sha256:<hex>    a registry digest, the same on every machine (repoDigest)
//	<tag>@sha256:<hex>           a local image id, only with PinLocal
//
// A local build is not reproducible: the same Dockerfile built on another
// machine gets another id, so a lock that pinned a local id would refuse
// every machine but the one that wrote it. Its build context is in the files
// hash, so a changed Dockerfile or file is still caught.
const (
	tagOnlyPrefix = "tag only: "
	digestSep     = "="
)

// FingerprintOptions is what fingerprints bind beyond the files: the part
// versions and the images. Parts maps "<kind>/<name>" to a version, as
// engine.PartVersions returns it; only the suite's grader and environment
// are read. Images holds the image roles the suite does not name itself
// with their tags; the lock binds the helper. ImageID returns the local
// image id of a tag and RepoDigests its registry digests (repo@sha256:...),
// empty for an image built locally; either nil means unknown. An image with
// a registry digest is recorded by that digest. One without is recorded by
// tag only, unless PinLocal records its local image id (strict, valid only
// on the machine or registry that built it).
type FingerprintOptions struct {
	Parts       map[string]string
	Images      map[string]string
	ImageID     func(tag string) (string, bool)
	RepoDigests func(tag string) []string
	PinLocal    bool
	// Pins are the library registry digests images were pushed as (tag to
	// <registry>/<repo>@sha256:<hex>, bench/imagepin). A pinned image is
	// recorded by that digest when the local image is that digest or cannot
	// be inspected here. Nil reads the suite's pins file
	// (imagepin.PinsFile), if it has one.
	Pins map[string]string
}

// LockPinsLocal reports whether l records any local image id, so a re-lock
// or an admission that was not told otherwise keeps pinning them.
func LockPinsLocal(l *schemas.SuiteLock) bool {
	if l == nil || l.Fingerprints == nil {
		return false
	}
	pinned := func(m map[string]string) bool {
		for _, v := range m {
			if _, id, _ := parseIdentity(v); id != "" {
				return true
			}
		}
		return false
	}
	if pinned(l.Fingerprints.Suite.Images) {
		return true
	}
	for _, t := range l.Fingerprints.Tasks {
		if pinned(t.Images) {
			return true
		}
	}
	return false
}

// Fingerprints computes the fingerprint section for the loaded suite s.
func Fingerprints(s *schemas.Suite, opts FingerprintOptions) (*schemas.LockFingerprints, error) {
	f := &schemas.LockFingerprints{
		Version: schemas.LockFingerprintsVersion,
		Tasks:   make(map[string]schemas.Fingerprint, len(s.Tasks)),
		Probes:  make(map[string]schemas.Fingerprint, len(s.Probes)),
	}
	if opts.Pins == nil && s.Root != "" {
		pins, err := imagepin.ReadPins(imagepin.PinsPath(s.Root))
		if err != nil {
			return nil, err
		}
		opts.Pins = pins
	}
	var err error
	if f.Suite, err = suiteFingerprint(s, opts); err != nil {
		return nil, fmt.Errorf("fingerprint suite %s: %w", s.ID, err)
	}
	for _, t := range s.Tasks {
		fp, err := taskFingerprint(s, t, opts)
		if err != nil {
			return nil, fmt.Errorf("fingerprint task %s: %w", t.ID, err)
		}
		f.Tasks[t.ID] = fp
	}
	for _, p := range s.Probes {
		fp, err := probeFingerprint(s, p)
		if err != nil {
			return nil, fmt.Errorf("fingerprint probe %s: %w", p.ID, err)
		}
		f.Probes[p.ID] = fp
	}
	f.Calibration = calibrationSetHash(s.Root) // W8 judge calibration: only when the suite has labels (docs/judge-calibration.md)
	return f, nil
}

// calibrationSetHash is the sha256 of the suite's calibration labels
// (calibration/labels.jsonl), "" when it has none, so a suite without a
// calibration set fingerprints as before. Records and snapshots beside the
// labels are left out: writing a record never makes a lock drift.
func calibrationSetHash(root string) string {
	sum, err := utils.SHA256File(filepath.Join(root, "calibration", "labels.jsonl"))
	if err != nil {
		return ""
	}
	return sum
}

// CheckFingerprints recomputes the fingerprints of s and compares them with
// the ones s.Lock recorded. A suite without a lock returns nothing (the
// caller refuses it on its own); a legacy lock returns no drift and the
// LegacyLockWarning. An image the lock pinned by id that cannot be inspected
// now is not checked and adds a warning, never drift, because CI and fresh
// clones have no images.
func CheckFingerprints(s *schemas.Suite, opts FingerprintOptions) ([]schemas.FingerprintDrift, []string, error) {
	if s == nil || s.Lock == nil {
		return nil, nil, nil
	}
	want := s.Lock.Fingerprints
	if want == nil {
		return nil, []string{LegacyLockWarning}, nil
	}
	if want.Version != schemas.LockFingerprintsVersion {
		return nil, nil, fmt.Errorf("suite %s lock fingerprints version %d is not %d, the version this binary checks; re-lock with this binary",
			s.ID, want.Version, schemas.LockFingerprintsVersion)
	}
	// The current images are compared live (compareImageLive): the files
	// and settings are recomputed with tags only, then each recorded
	// identity is checked against what docker reports for the tag now.
	tags := opts
	tags.ImageID, tags.RepoDigests = nil, nil
	got, err := Fingerprints(s, tags)
	if err != nil {
		return nil, nil, err
	}
	var drift []schemas.FingerprintDrift
	var warns []string
	cmp := func(scope, id string, w, g schemas.Fingerprint, inLock bool) {
		if !inLock {
			drift = append(drift, schemas.FingerprintDrift{Scope: scope, ID: id, Part: PartSettings, Fields: []string{"not fingerprinted"}})
			return
		}
		d, wn := compareFingerprint(scope, id, w, g, &opts)
		drift = append(drift, d...)
		warns = append(warns, wn...)
	}
	cmp(ScopeSuite, s.ID, want.Suite, got.Suite, true)
	for _, t := range s.Tasks {
		w, ok := want.Tasks[t.ID]
		cmp(ScopeTask, t.ID, w, got.Tasks[t.ID], ok)
	}
	for _, p := range s.Probes {
		w, ok := want.Probes[p.ID]
		cmp(ScopeProbe, p.ID, w, got.Probes[p.ID], ok)
	}
	if want.Calibration != got.Calibration { // W8 judge calibration: labels changed since the lock; a warning, never drift
		warns = append(warns, "calibration labels changed since the lock: run `suite lock` to bind the current set")
	}
	return drift, warns, nil
}

// DriftText is drift as one line for a refusal or a warning, naming each
// suite, task and probe and the part that drifted.
func DriftText(drift []schemas.FingerprintDrift) string {
	parts := make([]string, 0, len(drift))
	for _, d := range drift {
		msg := fmt.Sprintf("%s %s: %s changed", d.Scope, d.ID, d.Part)
		if len(d.Fields) > 0 {
			msg += " (" + strings.Join(d.Fields, ", ") + ")"
		}
		parts = append(parts, msg)
	}
	return strings.Join(parts, "; ")
}

// FingerprintHash is the sha256 of the canonical JSON of f, the value a
// manifest records as fingerprint_hash; empty for a legacy lock.
func FingerprintHash(f *schemas.LockFingerprints) (string, error) {
	if f == nil {
		return "", nil
	}
	return utils.HashJSON(f)
}

// AdmissionFingerprint is the sha256 of the suite and task fingerprints an
// admission of taskID checks: settings and files only, since an image id is
// machine local and is enforced on its own. Empty when f is nil or does not
// pin the task.
func AdmissionFingerprint(f *schemas.LockFingerprints, taskID string) string {
	if f == nil {
		return ""
	}
	t, ok := f.Tasks[taskID]
	if !ok {
		return ""
	}
	sum, err := utils.HashJSON(map[string]string{
		"suite.settings": f.Suite.Settings, "suite.files": f.Suite.Files,
		"task.settings": t.Settings, "task.files": t.Files,
	})
	if err != nil {
		return ""
	}
	return sum
}

// AdmissionCurrent reports whether rec still admits taskID under l: a
// record without a fingerprint (written before fingerprints existed) is
// taken as it is; one with a fingerprint must match the lock's.
func AdmissionCurrent(l *schemas.SuiteLock, taskID string, rec *schemas.AdmissionRecord) bool {
	if rec == nil || rec.Fingerprint == "" || l == nil {
		return true
	}
	return rec.Fingerprint == AdmissionFingerprint(l.Fingerprints, taskID)
}

// LegacyLockHash is the sha256 of l encoded without its fingerprints
// section: equal to the lock hash of the same lock before fingerprints were
// added, because adding them keeps created_at. It lets a manifest planned
// under the legacy lock resume or regate after `suite lock` adds the
// section.
func LegacyLockHash(l *schemas.SuiteLock) (string, error) {
	if l == nil {
		return "", nil
	}
	c := *l
	c.Fingerprints = nil
	data, err := encodeLock(&c)
	if err != nil {
		return "", err
	}
	return utils.SHA256Bytes(data), nil
}

// LockHashMatches reports whether the suite's lock is the one a manifest
// froze: the same lock hash, or the same lock with only a fingerprints
// section added since (LegacyLockHash).
func LockHashMatches(s *schemas.Suite, frozen string) bool {
	if s.LockHash == frozen {
		return true
	}
	if s.Lock == nil || s.Lock.Fingerprints == nil {
		return false
	}
	h, err := LegacyLockHash(s.Lock)
	return err == nil && h == frozen
}

func suiteFingerprint(s *schemas.Suite, opts FingerprintOptions) (schemas.Fingerprint, error) {
	c := *s
	c.Description, c.Imported, c.LockHash = "", nil, ""
	settings, err := canonicalSettings(&c)
	if err != nil {
		return schemas.Fingerprint{}, err
	}
	parts := map[string]any{}
	for _, k := range []string{"environment/" + s.Environment, "grader/" + s.Grader} {
		v, ok := opts.Parts[k]
		if !ok {
			v = "unregistered"
		}
		parts[k] = v
	}
	settings["parts"] = parts
	fp, err := settingsFingerprint(settings)
	if err != nil {
		return fp, err
	}
	var rels []string
	if s.BaseImage.Dockerfile != "" {
		rels = append(rels, path.Dir(filepath.ToSlash(s.BaseImage.Dockerfile)))
	}
	for _, dir := range []string{"fixtures", "services"} {
		if fi, err := os.Stat(filepath.Join(s.Root, dir)); err == nil && fi.IsDir() {
			rels = append(rels, dir)
		}
	}
	rels = append(rels, serviceFiles(s.Services)...)
	for _, r := range utils.ExternalAPIs(s, nil) {
		if _, have, err := utils.ExternalCassette(r); err == nil && have {
			rels = append(rels, r.API.Cassette)
		}
	}
	if rels, err = expandSuiteRoot(s.Root, rels); err != nil {
		return fp, err
	}
	if fp.Files, err = fingerprintFiles(s.Root, rels); err != nil {
		return fp, err
	}
	// The lock binds the toolchain images every grade depends on: the base
	// image and the helper. Tool proxy and service images, which `suite
	// admit` and image rebuilds replace often, are frozen per experiment
	// instead (Manifest.SuiteImages); their source is in Files.
	images := map[string]string{}
	if s.BaseImage.Tag != "" {
		images[ImageBase] = s.BaseImage.Tag
	}
	if tag := opts.Images[ImageHelper]; tag != "" {
		images[ImageHelper] = tag
	}
	fp.Images = identities(images, opts)
	return fp, nil
}

func taskFingerprint(s *schemas.Suite, t *schemas.Task, opts FingerprintOptions) (schemas.Fingerprint, error) {
	settings, err := canonicalSettings(t)
	if err != nil {
		return schemas.Fingerprint{}, err
	}
	fp, err := settingsFingerprint(settings)
	if err != nil {
		return fp, err
	}
	// Every file a task names must resolve inside its folder (resolveIn), so
	// the whole folder covers instruction, repo, verifier with answer and
	// original tests, controls, fixtures, notes, session files, skills,
	// service build contexts, cassettes and seed files, and none is missed.
	if fp.Files, err = fingerprintFiles(t.Dir, []string{"."}); err != nil {
		return fp, err
	}
	images := map[string]string{}
	if img, ok, err := utils.TaskImage(s.ID, t); err != nil {
		return fp, fmt.Errorf("task image: %w", err)
	} else if ok && img.Tag != "" {
		images[ImageTask] = img.Tag
	}
	svc, err := composeImages(s, t) // X2: a compose task's service images, bound like the task image (compose.go)
	if err != nil {
		return fp, err
	}
	for role, tag := range svc {
		images[role] = tag
	}
	fp.Images = identities(images, opts)
	return fp, nil
}

// probeFingerprint binds the probe YAML and every suite fixture its
// overlays read through {{suite.root}}. A path with a template variable past
// the suite root binds the folder before that variable, a superset of every
// file it can render to. Overlays without {{suite.root}} read task fixtures,
// which the task fingerprint binds.
func probeFingerprint(s *schemas.Suite, p *schemas.ProbeSpec) (schemas.Fingerprint, error) {
	settings, err := canonicalSettings(p)
	if err != nil {
		return schemas.Fingerprint{}, err
	}
	fp, err := settingsFingerprint(settings)
	if err != nil {
		return fp, err
	}
	var rels []string
	if p.Path != "" {
		rel, err := filepath.Rel(s.Root, p.Path)
		if err != nil {
			return fp, err
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	var overlays []schemas.OverlaySpec
	if p.Apply.Overlay != nil {
		overlays = append(overlays, *p.Apply.Overlay)
	}
	overlays = append(overlays, p.Apply.Overlays...)
	for _, o := range overlays {
		if r, ok := suiteFixture(o.From); ok {
			if _, err := os.Stat(filepath.Join(s.Root, filepath.FromSlash(r))); err == nil {
				rels = append(rels, r)
			}
		}
	}
	fp.Files, err = fingerprintFiles(s.Root, rels)
	return fp, err
}

// expandSuiteRoot replaces a build context that is the suite root itself
// (a Dockerfile beside suite.yaml) with the root's entries minus what the
// suite fingerprint must not read: the lock and the health sidecar it is
// written beside (hashing them would make every lock drift), tasks/ and
// probes/ (fingerprinted on their own), and run output folders.
func expandSuiteRoot(root string, rels []string) ([]string, error) {
	var out []string
	for _, rel := range rels {
		if path.Clean(filepath.ToSlash(rel)) != "." {
			out = append(out, rel)
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("read suite folder: %w", err)
		}
		for _, e := range entries {
			name := e.Name()
			switch {
			case name == LockFile, name == schemas.SuiteHealthFileName, name == tasksDir, name == probesDir,
				name == "runs", name == "checks", strings.HasPrefix(name, ".suite."),
				name == "calibration": // W8 judge calibration: hashed apart (calibrationSetHash)
				continue
			}
			out = append(out, name)
		}
	}
	return out, nil
}

// suiteFixture maps an overlay source under {{suite.root}} to the suite
// relative file or folder it reads.
func suiteFixture(from string) (string, bool) {
	const root = "{{suite.root}}/"
	rest, ok := strings.CutPrefix(strings.TrimSpace(from), root)
	if !ok || rest == "" {
		return "", false
	}
	if i := strings.Index(rest, "{{"); i >= 0 {
		rest = path.Dir(rest[:i] + "x")
	}
	rest = path.Clean(rest)
	if rest == "." || rest == ".." || strings.HasPrefix(rest, "../") {
		return "", false
	}
	return rest, true
}

func serviceFiles(services []schemas.ServiceSpec) []string {
	var rels []string
	for _, sv := range services {
		for _, dst := range sortedKeys(sv.Files) {
			rels = append(rels, sv.Files[dst])
		}
		if sv.Build != nil && sv.Build.Dockerfile != "" {
			rels = append(rels, sv.Build.Dockerfile, buildContext(sv.Build.Dockerfile, sv.Build.Context))
		}
	}
	return rels
}

// identities maps each role's tag to its identity (the forms above): the
// registry digest when the image has one, the local id only with PinLocal,
// else the tag only.
func identities(tags map[string]string, opts FingerprintOptions) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for role, tag := range tags {
		out[role] = tagOnlyPrefix + tag
		if p := pinnedDigest(tag, opts); p != "" {
			out[role] = tag + digestSep + p
			continue
		}
		if d := repoDigest(tag, opts); d != "" {
			out[role] = tag + digestSep + d
			continue
		}
		if opts.PinLocal && opts.ImageID != nil {
			if id, ok := opts.ImageID(tag); ok && id != "" {
				out[role] = tag + "@" + id
			}
		}
	}
	return out
}

// pinnedDigest is tag's library registry pin when the local image is the
// pushed digest (docker lists it among the image's registry digests), or
// when the image cannot be inspected here. A local image rebuilt since the
// push is not that digest: it is recorded as it is, never by a pin it
// does not match. On Docker's containerd store the pushed digest equals the
// local id, which repoDigest ignores; a pin is what tells the two apart.
func pinnedDigest(tag string, opts FingerprintOptions) string {
	p := opts.Pins[tag]
	if p == "" {
		return ""
	}
	if opts.ImageID != nil {
		id, ok := opts.ImageID(tag)
		if !ok {
			return p
		}
		if pinIsLocalID(p, id) {
			return p
		}
	}
	if opts.RepoDigests == nil {
		return p
	}
	for _, d := range opts.RepoDigests(tag) {
		if d == p {
			return p
		}
	}
	return ""
}

// pinIsLocalID reports whether a pin's digest is the local image id. On
// Docker's containerd store the id is the content digest of the image's
// manifest or index, the very digest a push reports, so equal means the
// same bytes even after the registry tag was removed. On the classic store
// the id is the config digest, never a manifest digest, so this never
// matches there by accident.
func pinIsLocalID(pinned, id string) bool {
	i := strings.LastIndex(pinned, "@")
	return i >= 0 && id != "" && pinned[i+1:] == id
}

// repoDigest picks the registry digest of tag: the one of the tag's own
// repository when there are several, else the first in sorted order. A
// digest equal to the local image id is not taken as one: Docker's
// containerd image store reports such a digest for every image, a local
// build included, so it says nothing about the image coming from a
// registry. On that store every image is recorded by tag only unless
// pinned; on the classic store a pulled image keeps its registry digest.
func repoDigest(tag string, opts FingerprintOptions) string {
	if opts.RepoDigests == nil {
		return ""
	}
	localID := ""
	if opts.ImageID != nil {
		localID, _ = opts.ImageID(tag)
	}
	var digests []string
	for _, d := range opts.RepoDigests(tag) {
		if i := strings.LastIndex(d, "@"); i >= 0 && d[i+1:] != localID {
			digests = append(digests, d)
		}
	}
	if len(digests) == 0 {
		return ""
	}
	sort.Strings(digests)
	repo := tag
	if i := strings.LastIndex(tag, ":"); i > strings.LastIndex(tag, "/") {
		repo = tag[:i]
	}
	for _, d := range digests {
		if strings.HasPrefix(d, repo+"@") {
			return d
		}
	}
	return digests[0]
}

// parseIdentity splits an image identity into its tag and either its local
// image id or its registry digest (both empty for a tag-only identity).
func parseIdentity(v string) (tag, id, digest string) {
	if rest, ok := strings.CutPrefix(v, tagOnlyPrefix); ok {
		return rest, "", ""
	}
	if i := strings.Index(v, digestSep); i >= 0 {
		return v[:i], "", v[i+1:]
	}
	if i := strings.LastIndex(v, "@sha256:"); i >= 0 {
		return v[:i], v[i+1:], ""
	}
	return v, "", ""
}

// compareImage compares two recorded identities (lock against lock): two
// digests or two local ids must be equal; otherwise only the tags count, so
// an identity changing from a local id to tag only of the same tag is no
// change.
func compareImage(want, got string) bool {
	wt, wid, wd := parseIdentity(want)
	gt, gid, gd := parseIdentity(got)
	switch {
	case wd != "" && gd != "":
		return wd != gd
	case wid != "" && gid != "":
		return wid != gid
	}
	return wt != gt
}

// compareImageLive checks a recorded identity against the image tag names
// now. A tag-only identity needs the same tag. A registry digest must be
// among the image's digests and a local id must be its id; when the image
// cannot be inspected here the identity is unchecked, never drift.
func compareImageLive(want, tag string, o *FingerprintOptions) (drift, unchecked bool) {
	wt, wid, wd := parseIdentity(want)
	if wt != tag {
		return true, false
	}
	if wid == "" && wd == "" {
		return false, false
	}
	if o.ImageID == nil {
		return false, true
	}
	id, ok := o.ImageID(tag)
	if !ok {
		return false, true
	}
	if wid != "" {
		return id != wid, false
	}
	if pinIsLocalID(wd, id) { // containerd store: the id is the pushed digest (pinIsLocalID)
		return false, false
	}
	if o.RepoDigests == nil {
		return false, true
	}
	for _, d := range o.RepoDigests(tag) {
		if d == wd {
			return false, false
		}
	}
	return true, false
}

// compareFingerprint lists the drift from want to got. With live set the
// images are checked against docker (compareImageLive) and got carries only
// tags; without, both sides are recorded identities (compareImage).
func compareFingerprint(scope, id string, want, got schemas.Fingerprint, live *FingerprintOptions) ([]schemas.FingerprintDrift, []string) {
	var drift []schemas.FingerprintDrift
	var warns []string
	if want.Settings != got.Settings {
		drift = append(drift, schemas.FingerprintDrift{Scope: scope, ID: id, Part: PartSettings, Fields: changedKeys(want.Fields, got.Fields)})
	}
	if want.Files != got.Files {
		drift = append(drift, schemas.FingerprintDrift{Scope: scope, ID: id, Part: PartFiles})
	}
	var roles []string
	for _, role := range sortedKeys(want.Images) {
		g, ok := got.Images[role]
		if !ok {
			if live != nil {
				warns = append(warns, fmt.Sprintf("%s %s: image %s not checked: this environment does not report it", scope, id, role))
			}
			continue
		}
		tag, _, _ := parseIdentity(g)
		if live == nil {
			if compareImage(want.Images[role], g) {
				roles = append(roles, role)
			}
			continue
		}
		switch d, unchecked := compareImageLive(want.Images[role], tag, live); {
		case d:
			roles = append(roles, role)
		case unchecked:
			warns = append(warns, fmt.Sprintf("%s %s: image %s (%s) not checked: it cannot be inspected here", scope, id, role, tag))
		}
	}
	if len(roles) > 0 {
		drift = append(drift, schemas.FingerprintDrift{Scope: scope, ID: id, Part: PartImage, Fields: roles})
	}
	return drift, warns
}

// carryImages keeps a recorded registry digest, or with pin a recorded
// local id, for a role whose image cannot be inspected now but still has the
// same tag, so re-locking on a machine without images never loses what a
// machine with them recorded. An image that can be inspected is recorded as
// it is now, so a re-lock without pinning drops local ids.
func carryImages(prev, cur map[string]string, opts FingerprintOptions) map[string]string {
	for role, v := range cur {
		old, ok := prev[role]
		if !ok {
			continue
		}
		ct, cid, cd := parseIdentity(v)
		ot, oid, od := parseIdentity(old)
		if ct != ot || cid != "" || cd != "" {
			continue
		}
		if opts.ImageID != nil {
			if _, ok := opts.ImageID(ct); ok {
				continue
			}
		}
		if od != "" || (opts.PinLocal && oid != "") {
			cur[role] = old
		}
	}
	return cur
}

func changedKeys(a, b map[string]string) []string {
	var out []string
	for _, k := range unionKeys(a, b) {
		if a[k] != b[k] {
			out = append(out, k)
		}
	}
	return out
}

// canonicalSettings is v as a JSON object with every empty value removed
// (null, "", false, 0, [] and {}), so a field added to a schema later, left
// empty, never changes a fingerprint, while any value set still does.
func canonicalSettings(v any) (map[string]any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	out, _ := prune(m).(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// prune drops empty values from objects recursively. Array elements are kept
// in place, since their position is part of the value.
func prune(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if p := prune(e); !isEmpty(p) {
				out[k] = p
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = prune(e)
		}
		return out
	default:
		return v
	}
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case json.Number:
		f, err := x.Float64()
		return err == nil && f == 0
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

func settingsFingerprint(settings map[string]any) (schemas.Fingerprint, error) {
	var fp schemas.Fingerprint
	sum, err := utils.HashJSON(settings)
	if err != nil {
		return fp, err
	}
	fp.Settings = sum
	fp.Fields = make(map[string]string, len(settings))
	for k, v := range settings {
		if fp.Fields[k], err = utils.HashJSON(v); err != nil {
			return fp, err
		}
	}
	return fp, nil
}

// fingerprintFiles hashes the files and folders rels, relative to base, in
// the utils.TreeHash line format, skipping volatile junk (fingerprintJunk).
// A path listed twice, or a file inside a listed folder, counts once.
func fingerprintFiles(base string, rels []string) (string, error) {
	files := map[string]string{}
	for _, rel := range rels {
		abs, err := resolveIn(base, rel)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", rel, err)
		}
		key := path.Clean(filepath.ToSlash(rel))
		if !fi.IsDir() {
			if fingerprintJunk(fi.Name(), false) {
				continue
			}
			sum, err := utils.SHA256File(abs)
			if err != nil {
				return "", err
			}
			files[key] = sum
			continue
		}
		if err := walkFingerprint(abs, key, files); err != nil {
			return "", err
		}
	}
	keys := sortedKeys(files)
	var buf bytes.Buffer
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte(0)
		buf.WriteString(files[k])
		buf.WriteByte('\n')
	}
	return utils.SHA256Bytes(buf.Bytes()), nil
}

func walkFingerprint(dir, key string, out map[string]string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && fingerprintJunk(d.Name(), d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		k := path.Join(key, filepath.ToSlash(rel))
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			out[k] = utils.SHA256Bytes([]byte("symlink:" + target))
		case !d.Type().IsRegular():
			out[k] = utils.SHA256Bytes([]byte("special:" + d.Type().String()))
		default:
			sum, err := utils.SHA256File(p)
			if err != nil {
				return err
			}
			out[k] = sum
		}
		return nil
	})
}

// fingerprintJunk reports whether a file or folder name is volatile junk
// the fingerprints skip: __pycache__ folders, compiled Python (*.pyc,
// *.pyo), .DS_Store and Thumbs.db, and editor swap and backup files (*.swp,
// *.swo, *~, .#*, #*#). The legacy lock fields still hash them.
func fingerprintJunk(name string, dir bool) bool {
	if dir {
		return name == "__pycache__"
	}
	switch {
	case name == ".DS_Store", name == "Thumbs.db",
		strings.HasSuffix(name, ".pyc"), strings.HasSuffix(name, ".pyo"),
		strings.HasSuffix(name, ".swp"), strings.HasSuffix(name, ".swo"),
		strings.HasSuffix(name, "~"), strings.HasPrefix(name, ".#"),
		len(name) > 1 && strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#"):
		return true
	}
	return false
}
