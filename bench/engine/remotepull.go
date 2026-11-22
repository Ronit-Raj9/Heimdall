package engine

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Private registry pulls (lane Y10): before a slot runs, the worker pulls
// the slot's agent and task images that live on a registry the lease
// carries a login for and that are not on this host yet. Each pull uses its
// own temporary Docker config (WorkerOptions.PullPrivate), so the login is
// used for that one pull and never kept.

// slotImageRefs lists the image references one slot starts: the arm's
// agent image (the per-task one when the arm built them) and the task's
// own image.
func slotImageRefs(exp *workerExp, slot *schemas.Slot) []string {
	if exp == nil || exp.m == nil || slot == nil {
		return nil
	}
	var refs []string
	if arm, ok := exp.m.Arms[slot.Arm]; ok && arm.Config.Image != "" {
		image := arm.Config.Image
		if tag, _ := taskAgentImage(&arm, slot.Task); tag != "" {
			image = tag
		}
		refs = append(refs, image)
	}
	if exp.suite != nil {
		if task := exp.suite.TaskByID(slot.Task); task != nil {
			if img, ok, err := utils.TaskImage(exp.suite.ID, task); err == nil && ok && img.Tag != "" {
				refs = append(refs, img.Tag)
			}
		}
	}
	return refs
}

// pullPrivateImages pulls the slot's missing images from the registries
// the lease has logins for. It returns a refusal reason when a pull fails.
func (w *Worker) pullPrivateImages(ctx context.Context, exp *workerExp, slot *schemas.Slot, auths []schemas.RegistryAuth) string {
	if w.opts.PullPrivate == nil || len(auths) == 0 {
		return ""
	}
	hosts := map[string]bool{}
	for _, a := range auths {
		hosts[schemas.NormalizeRegistryHost(a.Host)] = true
	}
	var ins ImageInspector
	if exp != nil {
		ins = w.imageInspector(exp.suite)
	}
	for _, ref := range slotImageRefs(exp, slot) {
		host := schemas.ImageRegistryHost(ref)
		if !hosts[host] {
			continue
		}
		if ins != nil {
			if id, err := ins.ImageID(ctx, ref); err == nil && id != "" {
				continue
			}
		}
		if err := w.opts.PullPrivate(ctx, ref, auths); err != nil {
			return "could not pull " + ref + " with the organization's login for " + host + " (Settings > Secrets): " + err.Error()
		}
		w.logger.Info("private image pulled", "image", ref, "registry", host)
	}
	return ""
}
