package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Runner hygiene (E6, docs/runners.md): what a remote or shared runner does
// so its disk never fills up. Every JanitorEvery it sweeps crashed runs'
// leftovers (the janitor), removes Heimdall images no run used for
// ImageMaxUnused, and prunes the build cache of Heimdall's own builder. The
// worker asks DiskState before every lease and takes none while the run
// store's file system is below MinFreeBytes.

// imageUsageFile records when the runner last used each image (by id), under
// the run store's .runner folder.
const imageUsageFile = "images.json"

// orgImageRe matches an organization-namespaced repository: org-<org id>/...
var orgImageRe = regexp.MustCompile(`^org-[a-z0-9_]{1,64}/`)

// neverGC are repository prefixes that are never removed even when labelled:
// the platform's own server and runner images.
var neverGC = []string{"heimdall/"}

// HygieneOptions configures a runner's Hygiene. Zero sizes and durations turn
// the step off; the caller applies the config defaults (lib.RunnerDiskConfig).
type HygieneOptions struct {
	StoreRoot      string        // the runner's run store: leases are read here and its file system is the one checked
	Every          time.Duration // sweep interval; 10 minutes when 0
	MinFreeBytes   int64         // free disk floor; 0: no floor
	ImageMaxUnused time.Duration // image GC age; 0: off
	Pinned         []string      // image references (tag or id) never removed
	Builder        string        // Heimdall's buildx builder; empty: no build cache prune
	BuildCacheKeep int64         // bytes of build cache kept in Builder; negative: off
	Logger         schemas.Logger
}

// Hygiene keeps one runner's disk healthy. It is safe for concurrent use.
type Hygiene struct {
	janitor *Janitor
	docker  utils.Docker
	opts    HygieneOptions
	logger  schemas.Logger
	now     func() time.Time
	free    func(path string) (int64, error)

	mu       sync.Mutex
	used     map[string]time.Time // image id -> last used
	loaded   bool
	lowShown bool
}

// NewHygiene returns a runner's hygiene. janitor may be nil (no leftovers
// sweep).
func NewHygiene(janitor *Janitor, docker utils.Docker, opts HygieneOptions) *Hygiene {
	if opts.Logger == nil {
		opts.Logger = schemas.NoopLogger{}
	}
	if opts.Every <= 0 {
		opts.Every = 10 * time.Minute
	}
	return &Hygiene{janitor: janitor, docker: docker, opts: opts, logger: opts.Logger, now: time.Now, free: diskFree, used: map[string]time.Time{}}
}

// Run sweeps at once and then every Every until ctx ends.
func (h *Hygiene) Run(ctx context.Context) {
	t := time.NewTicker(h.opts.Every)
	defer t.Stop()
	for {
		h.Sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// HygieneReport is what one sweep did.
type HygieneReport struct {
	Leftovers  *SweepReport `json:"leftovers,omitempty"`
	Images     int          `json:"images"`
	BuildCache bool         `json:"build_cache"`
}

// Sweep runs the janitor over runs whose lease is not fresh, then image GC
// and the build cache prune. Every step is attempted; failures are logged.
func (h *Hygiene) Sweep(ctx context.Context) HygieneReport {
	var rep HygieneReport
	if h.janitor != nil && h.opts.StoreRoot != "" {
		lv, err := ScanRunLiveness([]string{h.opts.StoreRoot}, DefaultStaleAfter, h.now())
		if err != nil {
			h.logger.Warn("runner janitor skipped: leases could not be read", "error", err.Error())
		} else {
			r, err := h.janitor.Sweep(ctx, lv)
			rep.Leftovers = r
			if err != nil {
				h.logger.Warn("runner janitor sweep failed", "error", err.Error())
			}
		}
	}
	if h.opts.ImageMaxUnused > 0 {
		n, err := h.GCImages(ctx)
		rep.Images = n
		if err != nil {
			h.logger.Warn("runner image cleanup failed", "error", err.Error())
		}
	}
	if ok, err := h.PruneBuildCache(ctx); err != nil {
		h.logger.Warn("runner build cache prune failed", "error", err.Error())
	} else {
		rep.BuildCache = ok
	}
	return rep
}

// DiskState reports the run store's free disk against the floor. A file
// system that cannot be read is reported as not low, with FreeBytes -1: the
// floor never blocks a runner on a platform it cannot measure.
func (h *Hygiene) DiskState() schemas.WorkerDisk {
	d := schemas.WorkerDisk{FreeBytes: -1, MinFreeBytes: h.opts.MinFreeBytes}
	if h.opts.StoreRoot == "" {
		return d
	}
	free, err := h.free(existingParent(h.opts.StoreRoot))
	if err != nil {
		return d
	}
	d.FreeBytes = free
	d.Low = h.opts.MinFreeBytes > 0 && free < h.opts.MinFreeBytes
	h.mu.Lock()
	show := d.Low != h.lowShown
	h.lowShown = d.Low
	h.mu.Unlock()
	if show && d.Low {
		h.logger.Warn("free disk below runner.disk.min_free_gb: taking no work until space is freed", "free_gb", gb(free), "min_free_gb", gb(h.opts.MinFreeBytes), "store", h.opts.StoreRoot)
	} else if show {
		h.logger.Info("free disk back above runner.disk.min_free_gb: taking work again", "free_gb", gb(free))
	}
	return d
}

func gb(b int64) string { return fmt.Sprintf("%.1f", float64(b)/(1<<30)) }

// existingParent is p, or its nearest folder that exists (a run store not
// created yet is measured on the file system it will live on).
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// NoteImages records that a run used these images (ids or references) now.
func (h *Hygiene) NoteImages(ids ...string) {
	if len(ids) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadLocked()
	now := h.now().UTC()
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			h.used[id] = now
		}
	}
	h.saveLocked()
}

func (h *Hygiene) usagePath() string {
	if h.opts.StoreRoot == "" {
		return ""
	}
	return filepath.Join(h.opts.StoreRoot, ".runner", imageUsageFile)
}

func (h *Hygiene) loadLocked() {
	if h.loaded {
		return
	}
	h.loaded = true
	p := h.usagePath()
	if p == "" {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.logger.Warn("read runner image usage", "error", err.Error())
		}
		return
	}
	var m map[string]time.Time
	if json.Unmarshal(raw, &m) == nil {
		for k, v := range m {
			h.used[k] = v
		}
	}
}

func (h *Hygiene) saveLocked() {
	p := h.usagePath()
	if p == "" {
		return
	}
	raw, err := json.Marshal(h.used)
	if err == nil {
		err = os.MkdirAll(filepath.Dir(p), 0o700)
	}
	if err == nil {
		err = writeUsageFile(p, raw)
	}
	if err != nil {
		h.logger.Warn("write runner image usage", "error", err.Error())
	}
}

// gcImage is one labelled image with all its references.
type gcImage struct {
	id      string
	refs    []string
	created time.Time
}

// GCImages removes Heimdall images (label bifrost-bench=1, every tag under
// bench/ or org-<org id>/) that no run used for ImageMaxUnused: last use is
// the later of the runner's own record and the image's creation time. An
// image any container uses (running or stopped), a pinned image, a run
// snapshot image (the janitor's) and an untagged one are never removed. A
// listing that fails removes nothing.
func (h *Hygiene) GCImages(ctx context.Context) (int, error) {
	imgs, err := h.listImages(ctx)
	if err != nil {
		return 0, err
	}
	if len(imgs) == 0 {
		return 0, nil
	}
	inUse, err := h.imagesInUse(ctx)
	if err != nil {
		return 0, err
	}
	pinned := map[string]bool{}
	for _, p := range h.opts.Pinned {
		pinned[strings.TrimSpace(p)] = true
	}
	h.mu.Lock()
	h.loadLocked()
	used := make(map[string]time.Time, len(h.used))
	for k, v := range h.used {
		used[k] = v
	}
	h.mu.Unlock()
	now := h.now()
	var drop []gcImage
	for _, img := range imgs {
		if !gcEligible(img, inUse, pinned) {
			continue
		}
		last := img.created
		for _, k := range append([]string{img.id}, img.refs...) {
			if t, ok := used[k]; ok && t.After(last) {
				last = t
			}
		}
		if last.IsZero() || now.Sub(last) < h.opts.ImageMaxUnused {
			continue
		}
		drop = append(drop, img)
	}
	n := 0
	var errs []error
	for _, img := range drop {
		// By reference, without -f: docker refuses an image a container
		// started since the listing, instead of untagging it under that run.
		args := append([]string{"image", "rm"}, img.refs...)
		if _, err := h.docker.MustRun(ctx, args...); err != nil {
			errs = append(errs, fmt.Errorf("remove image %s: %w", img.refs[0], err))
			continue
		}
		n++
	}
	if n > 0 {
		h.mu.Lock()
		for _, img := range drop {
			delete(h.used, img.id)
			for _, r := range img.refs {
				delete(h.used, r)
			}
		}
		h.saveLocked()
		h.mu.Unlock()
		h.logger.Info("runner removed unused images", "images", n, "unused_for", h.opts.ImageMaxUnused.String())
	}
	return n, errors.Join(errs...)
}

// gcEligible reports whether an image may be removed at all.
func gcEligible(img gcImage, inUse, pinned map[string]bool) bool {
	if len(img.refs) == 0 || inUse[img.id] || pinned[img.id] {
		return false
	}
	for _, r := range img.refs {
		if pinned[r] || inUse[r] || !heimdallRef(r) {
			return false
		}
	}
	return true
}

// heimdallRef reports whether a reference is a Heimdall task or agent image:
// under bench/ or an organization namespace, never the platform's own.
func heimdallRef(ref string) bool {
	for _, p := range neverGC {
		if strings.HasPrefix(ref, p) {
			return false
		}
	}
	return strings.HasPrefix(ref, "bench/") || orgImageRe.MatchString(ref)
}

// listImages lists labelled images without a run label, grouped by id.
func (h *Hygiene) listImages(ctx context.Context) ([]gcImage, error) {
	res, err := h.docker.MustRun(ctx, "image", "ls", "--no-trunc", "--filter", "label="+utils.LabelBench+"=1",
		"--format", "{{.ID}}\t{{.Repository}}:{{.Tag}}\t{{.CreatedAt}}")
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	snap, err := h.docker.MustRun(ctx, "image", "ls", "-q", "--no-trunc", "--filter", "label="+utils.LabelRun)
	if err != nil {
		return nil, fmt.Errorf("list snapshot images: %w", err)
	}
	snapshots := map[string]bool{}
	for _, id := range strings.Fields(string(snap.Stdout)) {
		snapshots[id] = true
	}
	return parseImageList(res.Stdout, snapshots), nil
}

// parseImageList reads "<id>\t<repo>:<tag>\t<created>" lines into images,
// skipping snapshot ids and dangling references. It is pure.
func parseImageList(out []byte, skip map[string]bool) []gcImage {
	by := map[string]*gcImage{}
	var order []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(f) < 2 || f[0] == "" || skip[f[0]] {
			continue
		}
		img, ok := by[f[0]]
		if !ok {
			img = &gcImage{id: f[0]}
			if len(f) > 2 {
				img.created, _ = parseDockerTime(f[2])
			}
			by[f[0]] = img
			order = append(order, f[0])
		}
		if ref := f[1]; !strings.Contains(ref, "<none>") {
			img.refs = append(img.refs, ref)
		}
	}
	out2 := make([]gcImage, 0, len(order))
	for _, id := range order {
		img := by[id]
		sort.Strings(img.refs)
		out2 = append(out2, *img)
	}
	return out2
}

// imagesInUse is every image id and reference a container (any state) uses.
func (h *Hygiene) imagesInUse(ctx context.Context) (map[string]bool, error) {
	res, err := h.docker.MustRun(ctx, "ps", "-a", "-q", "--no-trunc")
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	ids := strings.Fields(string(res.Stdout))
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	ins, err := h.docker.MustRun(ctx, append([]string{"container", "inspect", "--format", "{{.Image}}\t{{.Config.Image}}"}, ids...)...)
	if err != nil {
		return nil, fmt.Errorf("inspect containers: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(ins.Stdout))
	for sc.Scan() {
		for _, f := range strings.Split(strings.TrimSpace(sc.Text()), "\t") {
			if f = strings.TrimSpace(f); f != "" {
				out[f] = true
			}
		}
	}
	for ref := range out { // a container started from "bench/x" uses "bench/x:latest"
		if !strings.HasPrefix(ref, "sha256:") && !strings.Contains(refTail(ref), ":") {
			out[ref+":latest"] = true
		}
	}
	return out, nil
}

// refTail is the last path segment of a reference (a registry port is not a
// tag).
func refTail(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ref
}

// PruneBuildCache prunes Heimdall's own builder down to BuildCacheKeep bytes.
// Without a builder it does nothing: the default builder's cache belongs to
// every project on the host.
func (h *Hygiene) PruneBuildCache(ctx context.Context) (bool, error) {
	b := strings.TrimSpace(h.opts.Builder)
	if b == "" || h.opts.BuildCacheKeep < 0 {
		return false, nil
	}
	keep := fmt.Sprintf("%dmb", h.opts.BuildCacheKeep>>20)
	if _, err := h.docker.MustRun(ctx, "buildx", "prune", "--builder", b, "--force", "--keep-storage", keep); err != nil {
		return false, fmt.Errorf("buildx prune --builder %s: %w", b, err)
	}
	return true, nil
}

// writeUsageFile replaces p atomically (temp file in the same folder, then
// rename), mode 0600.
func writeUsageFile(p string, b []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}
