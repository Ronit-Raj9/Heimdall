package imagepin

import (
	"context"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Want is one image a suite lock pins by registry digest.
type Want struct {
	Tag    string `json:"tag"`
	Pinned string `json:"pinned"`
}

// LockedImages lists the distinct images the suite's lock pins by registry
// digest: the suite's roles and every task's. A lock that records images by
// tag only lists nothing (those are built locally, as before).
func LockedImages(s *schemas.Suite) []Want {
	if s == nil || s.Lock == nil || s.Lock.Fingerprints == nil {
		return nil
	}
	seen := map[string]string{}
	add := func(m map[string]string) {
		for _, v := range m {
			if tag, pinned, ok := utils.LockedPin(v); ok {
				seen[tag] = pinned
			}
		}
	}
	add(s.Lock.Fingerprints.Suite.Images)
	for _, fp := range s.Lock.Fingerprints.Tasks {
		add(fp.Images)
	}
	out := make([]Want, 0, len(seen))
	for t, p := range seen {
		out = append(out, Want{Tag: t, Pinned: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

// Prefetch states.
const (
	PrefetchWarm    = "warm"    // already here, the pinned digest
	PrefetchPulled  = "pulled"  // pulled by digest and tagged now
	PrefetchDiffers = "differs" // a local image of that tag is another digest: left alone, the lock check refuses it
	PrefetchFailed  = "failed"
)

// PrefetchResult is one image of a prefetch.
type PrefetchResult struct {
	Want
	State   string        `json:"state"`
	Error   string        `json:"error,omitempty"`
	Elapsed time.Duration `json:"elapsed_ns"`
}

// Prefetch pulls, concurrency at a time, every wanted image the machine
// lacks, by its pinned digest, and tags it, so a lease starts on a warm
// image instead of a cold build. env is layered over docker's environment
// (a private DOCKER_CONFIG, or nil for the machine's own login). A local
// image of the same tag but another digest is never replaced: that is
// drift the suite lock check reports.
func Prefetch(ctx context.Context, d utils.Docker, env []string, wants []Want, concurrency int, log io.Writer) []PrefetchResult {
	if concurrency < 1 {
		concurrency = 1
	}
	if log == nil {
		log = io.Discard
	}
	out := make([]PrefetchResult, len(wants))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var logMu sync.Mutex
	for i, w := range wants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			start := time.Now()
			r := PrefetchResult{Want: w}
			present, same, err := d.HasPinned(ctx, w.Tag, w.Pinned)
			switch {
			case err != nil:
				r.State, r.Error = PrefetchFailed, err.Error()
			case present && same:
				r.State = PrefetchWarm
			case present:
				r.State = PrefetchDiffers
			default:
				if err := d.PullPinned(ctx, env, w.Tag, w.Pinned, nil); err != nil {
					r.State, r.Error = PrefetchFailed, err.Error()
				} else {
					r.State = PrefetchPulled
				}
			}
			r.Elapsed = time.Since(start)
			out[i] = r
			logMu.Lock()
			line := "  " + r.State + "  " + w.Tag
			if r.Error != "" {
				line += ": " + r.Error
			}
			io.WriteString(log, line+"\n")
			logMu.Unlock()
		}()
	}
	wg.Wait()
	return out
}
