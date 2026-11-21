package lib

import (
	"fmt"
	"time"
)

// Runner disk defaults (runner.disk). A runner refuses leases below
// DefaultMinFreeGB free on its run store, removes Heimdall's task and agent
// images no run used for DefaultImageMaxUnusedH hours, and keeps at most
// DefaultBuildCacheKeepGB of build cache in Heimdall's own builder.
const (
	DefaultMinFreeGB        = 10
	DefaultImageMaxUnusedH  = 48
	DefaultBuildCacheKeepGB = 10
	DefaultJanitorEveryMin  = 10
)

// maxSlotsPerProvider bounds gateway.slots_per_provider: every slot is a
// provider key row in Bifrost and a slot in the fault proxy.
const maxSlotsPerProvider = 256

// RunnerConfig is the runner section: what a runner machine does to keep its
// disk from filling up (docs/runners.md).
type RunnerConfig struct {
	Disk RunnerDiskConfig `yaml:"disk" json:"disk,omitzero"`
}

// RunnerDiskConfig is runner.disk. Zero values take the defaults above; a
// negative value turns that step off.
type RunnerDiskConfig struct {
	// MinFreeGB is the free space the run store's file system must keep: below
	// it the runner takes no lease and reports disk_low to the server.
	MinFreeGB int `yaml:"min_free_gb" json:"min_free_gb,omitempty"`
	// ImageMaxUnusedH removes Heimdall task and agent images (bench/ and
	// organization tags only) that no run used for this many hours. Images
	// in use by a container and pinned images are never removed.
	ImageMaxUnusedH int `yaml:"image_max_unused_h" json:"image_max_unused_h,omitempty"`
	// PinnedImages are image references (tag or id) image GC never removes.
	PinnedImages []string `yaml:"pinned_images" json:"pinned_images,omitempty"`
	// Builder is Heimdall's own buildx builder. Build cache is pruned only in
	// it (down to BuildCacheKeepGB); empty reads BUILDX_BUILDER, and with
	// neither set the build cache is left alone, since the default builder is
	// shared with every other project on the host.
	Builder          string `yaml:"builder" json:"builder,omitempty"`
	BuildCacheKeepGB int    `yaml:"build_cache_keep_gb" json:"build_cache_keep_gb,omitempty"`
	// KeepRunFolders keeps a run's local folder after the server accepted its
	// upload. Off by default: the server holds the sealed copy.
	KeepRunFolders bool `yaml:"keep_run_folders" json:"keep_run_folders,omitempty"`
	// JanitorEveryMin is how often the runner sweeps leftovers, images and
	// build cache.
	JanitorEveryMin int `yaml:"janitor_every_min" json:"janitor_every_min,omitempty"`
}

// Validate checks runner.disk.
func (r RunnerConfig) Validate() error {
	d := r.Disk
	if d.MinFreeGB > 1<<20 || d.BuildCacheKeepGB > 1<<20 {
		return fmt.Errorf("runner.disk sizes are in GB; %d/%d is too large", d.MinFreeGB, d.BuildCacheKeepGB)
	}
	if d.JanitorEveryMin > 24*60 {
		return fmt.Errorf("runner.disk.janitor_every_min must be at most 1440, got %d", d.JanitorEveryMin)
	}
	return nil
}

// MinFreeBytes is the free disk floor in bytes; 0 means no floor.
func (d RunnerDiskConfig) MinFreeBytes() int64 {
	return gbOrDefault(d.MinFreeGB, DefaultMinFreeGB)
}

// BuildCacheKeepBytes is the build cache to keep in Heimdall's builder; -1
// means the prune is off.
func (d RunnerDiskConfig) BuildCacheKeepBytes() int64 {
	if d.BuildCacheKeepGB < 0 {
		return -1
	}
	return gbOrDefault(d.BuildCacheKeepGB, DefaultBuildCacheKeepGB)
}

// ImageMaxUnused is how long an unused image is kept; 0 means image GC is off.
func (d RunnerDiskConfig) ImageMaxUnused() time.Duration {
	switch {
	case d.ImageMaxUnusedH < 0:
		return 0
	case d.ImageMaxUnusedH == 0:
		return DefaultImageMaxUnusedH * time.Hour
	}
	return time.Duration(d.ImageMaxUnusedH) * time.Hour
}

// JanitorEvery is the sweep interval.
func (d RunnerDiskConfig) JanitorEvery() time.Duration {
	if d.JanitorEveryMin <= 0 {
		return DefaultJanitorEveryMin * time.Minute
	}
	return time.Duration(d.JanitorEveryMin) * time.Minute
}

func gbOrDefault(gb, def int) int64 {
	switch {
	case gb < 0:
		return 0
	case gb == 0:
		gb = def
	}
	return int64(gb) << 30
}

// expandSlots applies gateway.slots_per_provider: fp-slot-1..N for the
// anthropic provider and oa-slot-1..N for openai, replacing the lists.
func (g *GatewayConfig) expandSlots() {
	if g.SlotsPerProvider <= 0 || g.SlotsPerProvider > maxSlotsPerProvider {
		return
	}
	g.KeySlots, g.OpenAIKeySlots = SlotNames("fp", g.SlotsPerProvider), SlotNames("oa", g.SlotsPerProvider)
}

func (g GatewayConfig) validateSlots() error {
	if g.SlotsPerProvider < 0 || g.SlotsPerProvider > maxSlotsPerProvider {
		return fmt.Errorf("gateway.slots_per_provider must be 0 to %d, got %d", maxSlotsPerProvider, g.SlotsPerProvider)
	}
	if g.OrgSlotsPerProvider < 0 || g.OrgSlotsPerProvider > maxSlotsPerProvider {
		return fmt.Errorf("gateway.org_slots_per_provider must be 0 to %d, got %d", maxSlotsPerProvider, g.OrgSlotsPerProvider)
	}
	return nil
}

// SlotNames returns <prefix>-slot-1..n, the names the fault proxy and the
// gateway agree on.
func SlotNames(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, fmt.Sprintf("%s-slot-%d", prefix, i))
	}
	return out
}
