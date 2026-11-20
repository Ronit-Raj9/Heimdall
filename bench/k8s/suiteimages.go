package k8s

import (
	"context"
	"fmt"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Frozen evaluator images (docs/lock-fingerprints.md) on Kubernetes. The
// manifest freezes the image ids of the suite base, helper and collector
// images (Manifest.SuiteImages). Create refuses a run whose controller tags
// now name other images, as the Docker driver does, and Exec checks the ids
// the node reports for the helper containers (before the workspace is
// seeded) and the collector, so a changed evaluator never seeds, observes,
// exports or grades a frozen experiment.

// roleCollector is the image role of the agent event collector, frozen
// whenever this driver runs it, with or without services.
const roleCollector = "collector"

// helperRole is the frozen role of the helper image this run uses: the
// suite base image when no helper image is configured.
func (d *Driver) helperRole() string {
	if d.cfg.HelperImage == "" {
		return suite.ImageBase
	}
	return suite.ImageHelper
}

// roleTags maps each frozen role this run starts to its tag.
func (d *Driver) roleTags(rc *schemas.RunContext) map[string]string {
	tags := map[string]string{}
	if rc.Suite != nil && rc.Suite.BaseImage.Tag != "" {
		tags[suite.ImageBase] = rc.Suite.BaseImage.Tag
	}
	if d.cfg.HelperImage != "" {
		tags[suite.ImageHelper] = d.cfg.HelperImage
	}
	if d.cfg.EventsCollector && d.cfg.CollectorImage != "" {
		tags[roleCollector] = d.cfg.CollectorImage
	}
	return tags
}

// checkFrozenRoles refuses a run whose controller tag for a frozen role now
// names another image id. A role that cannot be inspected here is not
// checked and logs a warning (the node check in Exec still applies).
func (d *Driver) checkFrozenRoles(ctx context.Context, rc *schemas.RunContext) error {
	if rc == nil || rc.Manifest == nil || len(rc.Manifest.SuiteImages) == 0 {
		return nil
	}
	tags := d.roleTags(rc)
	roles := make([]string, 0, len(tags))
	for role := range tags {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		want := rc.Manifest.SuiteImages[role]
		if want == "" {
			continue
		}
		got, err := d.ImageID(ctx, tags[role])
		if err != nil || got == "" {
			d.cfg.Logger.Warn("frozen image not checked on the controller: it cannot be inspected", "experiment", rc.Manifest.ExpID, "role", role, "image", tags[role])
			continue
		}
		if got != want {
			return fmt.Errorf("%s image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
				role, tags[role], got, rc.Manifest.ExpID, want)
		}
	}
	return nil
}

// checkNodeRole refuses a container whose node-reported image id is not the
// image frozen for role. A role without a frozen id, or a container not
// reported yet, is not checked.
func (d *Driver) checkNodeRole(ctx context.Context, rc *schemas.RunContext, rec *record, role, tag, container, nodeID string) error {
	if rc == nil || rc.Manifest == nil || tag == "" {
		return nil
	}
	want := rc.Manifest.SuiteImages[role]
	if want == "" || nodeID == "" {
		return nil
	}
	if d.agentImageFrozen(ctx, rec, nodeID, want, tag) {
		return nil
	}
	return fmt.Errorf("%s container image on node %s is %s, but experiment %s was planned with %s %s; load or push the planned image",
		container, rec.Node, nodeID, rc.Manifest.ExpID, role, want)
}

// checkHelperOnNode checks the prep container's helper image on the node
// before anything is seeded with it.
func (d *Driver) checkHelperOnNode(ctx context.Context, rc *schemas.RunContext, r runRef, helper string) error {
	if rc == nil || rc.Manifest == nil || rc.Manifest.SuiteImages[d.helperRole()] == "" {
		return nil
	}
	p, err := d.getPod(ctx, r)
	if err != nil {
		return fmt.Errorf("read pod: %w", err)
	}
	rec := &record{Node: p.Spec.NodeName}
	for _, cs := range p.Status.InitContainerStatuses {
		if cs.Name == ctrPrep {
			return d.checkNodeRole(ctx, rc, rec, d.helperRole(), helper, ctrPrep, cs.ImageID)
		}
	}
	return nil
}
