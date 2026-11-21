package k8s

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Namespace pool mode (docs/kubernetes.md, "Namespace pool"). When
// kubernetes.namespaces lists namespaces an administrator created up front,
// the driver never creates or deletes a namespace. Each run (and each
// egress probe) leases one free pool namespace: the lease is the run's
// anchor config map, and creating it is the lock. A create that conflicts
// means the namespace is busy; the driver tries the next one. Every object
// a run makes in its namespace is owned by the anchor, so deleting the
// anchor releases the namespace; Destroy also deletes the run's fixed-name
// objects itself and waits until the pod is gone, since the next run reuses
// the same names.

// poolGoneTimeout bounds the wait for a released namespace's run objects.
const poolGoneTimeout = 2 * time.Minute

// poolObjects are the fixed-name objects a run or probe makes in its
// namespace, other than the anchor and the service account.
var poolObjects = []struct{ collection, name string }{
	{"pods", podName}, {"pods", probePodName},
	{"secrets", envSecret}, {"configmaps", scriptsMap},
	{"netpol", denyPolicy}, {"netpol", egressPolicy},
}

const probePodName = "egress-probe"

// poolMode reports whether the driver leases namespaces from a pool.
func (d *Driver) poolMode() bool { return len(d.cfg.Namespaces) > 0 }

func anchorPath(ns string) string { return corePath(ns, "configmaps") + "/" + anchorName }

func poolObjectPath(ns, collection, name string) string {
	if collection == "netpol" {
		return netpolPath(ns) + "/" + name
	}
	return corePath(ns, collection) + "/" + name
}

// anchorRun is the run id an anchor names, by its label or its data.
func anchorRun(cm ConfigMap) string {
	if r := cm.Metadata.Labels[LabelRun]; r != "" {
		return r
	}
	return cm.Data["run_id"]
}

// remember records a leased namespace for later calls of the same run.
func (d *Driver) remember(runID, ns string) {
	d.poolMu.Lock()
	defer d.poolMu.Unlock()
	if d.leases == nil {
		d.leases = map[string]string{}
	}
	d.leases[runID] = ns
}

func (d *Driver) forget(runID string) {
	d.poolMu.Lock()
	defer d.poolMu.Unlock()
	delete(d.leases, runID)
}

// poolNamespaceOf finds the pool namespace runID holds: the process-local
// lease map first, then the pool namespaces' anchors (after a restart). ok
// is false when no pool namespace holds an anchor of the run.
func (d *Driver) poolNamespaceOf(ctx context.Context, runID string) (string, bool, error) {
	d.poolMu.Lock()
	ns, ok := d.leases[runID]
	d.poolMu.Unlock()
	if ok {
		return ns, true, nil
	}
	var errs []error
	for _, ns := range d.cfg.Namespaces {
		var cm ConfigMap
		err := d.cfg.Client.Get(ctx, anchorPath(ns), &cm)
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", ns, err))
			continue
		}
		if anchorRun(cm) == runID {
			d.remember(runID, ns)
			return ns, true, nil
		}
	}
	return "", false, errors.Join(errs...)
}

// leasePool takes a free pool namespace for runID by creating its anchor
// with labels and data, and returns the namespace and the anchor's owner
// reference. A namespace whose anchor already names runID is an earlier
// attempt of the same run: its anchor and objects are removed and the
// anchor is created again. Any other anchor means busy.
func (d *Driver) leasePool(ctx context.Context, runID string, labels, data map[string]string) (string, []OwnerReference, error) {
	labels = d.ownedLabels(maps.Clone(labels))
	cm := ConfigMap{APIVersion: "v1", Kind: "ConfigMap", Metadata: ObjectMeta{Name: anchorName, Labels: labels}, Data: data}
	order := d.cfg.Namespaces
	if prev, ok, _ := d.poolNamespaceOf(ctx, runID); ok { // the earlier attempt's namespace first
		order = append([]string{prev}, order...)
	}
	var errs []error
	tried := map[string]bool{}
	for _, ns := range order {
		if tried[ns] {
			continue
		}
		tried[ns] = true
		owner, err := d.tryLease(ctx, ns, runID, cm)
		if err == nil {
			d.remember(runID, ns)
			return ns, owner, nil
		}
		if !errors.Is(err, errBusy) {
			errs = append(errs, fmt.Errorf("namespace %s: %w", ns, err))
		}
	}
	msg := fmt.Sprintf("no free namespace in kubernetes.namespaces (%s): every one is leased by another run", strings.Join(d.cfg.Namespaces, ", "))
	if len(errs) > 0 {
		return "", nil, fmt.Errorf("%s or unusable: %w", msg, errors.Join(errs...))
	}
	return "", nil, errors.New(msg + "; wait for a run to finish, add namespaces, or lower the concurrency")
}

var errBusy = errors.New("namespace busy")

func (d *Driver) tryLease(ctx context.Context, ns, runID string, cm ConfigMap) ([]OwnerReference, error) {
	got, err := d.createPoolAnchor(ctx, ns, cm)
	if err == nil {
		return got, d.purgePool(ctx, ns, "", got[0].UID)
	}
	if !IsConflict(err) {
		return nil, err
	}
	var cur ConfigMap
	if err := d.cfg.Client.Get(ctx, anchorPath(ns), &cur); err != nil {
		if IsNotFound(err) { // released meanwhile; let the next lease take it
			return nil, errBusy
		}
		return nil, err
	}
	if anchorRun(cur) != runID {
		return nil, errBusy
	}
	d.cfg.Logger.Warn("leftover anchor of this run in the pool namespace; replacing it", "namespace", ns, "run", runID)
	if err := d.releasePool(ctx, ns, runID, cur.Metadata.UID); err != nil {
		return nil, fmt.Errorf("remove leftover anchor: %w", err)
	}
	got, err = d.createPoolAnchor(ctx, ns, cm)
	if IsConflict(err) {
		return nil, errBusy
	}
	if err != nil {
		return nil, err
	}
	return got, d.purgePool(ctx, ns, "", got[0].UID)
}

func (d *Driver) createPoolAnchor(ctx context.Context, ns string, cm ConfigMap) ([]OwnerReference, error) {
	var got ConfigMap
	if err := d.cfg.Client.Create(ctx, corePath(ns, "configmaps"), cm, &got); err != nil {
		return nil, err
	}
	if got.Metadata.UID == "" {
		return nil, errors.New("anchor: the API server returned no uid")
	}
	return []OwnerReference{{APIVersion: "v1", Kind: "ConfigMap", Name: anchorName, UID: got.Metadata.UID, BlockOwnerDeletion: boolPtr(false)}}, nil
}

// releasePool deletes the anchor (only the object with uid, when uid is
// set) and the run's objects, and waits until they are gone. The namespace
// itself is never touched.
func (d *Driver) releasePool(ctx context.Context, ns, runID, uid string) error {
	var err error
	if uid != "" {
		err = d.cfg.Client.DeleteUID(ctx, anchorPath(ns), uid)
	} else {
		err = d.cfg.Client.Delete(ctx, anchorPath(ns))
	}
	if err != nil {
		return err
	}
	if err := d.purgePool(ctx, ns, runID, ""); err != nil {
		return err
	}
	if uid != "" {
		var cur ConfigMap
		if gerr := d.cfg.Client.Get(ctx, anchorPath(ns), &cur); gerr == nil && cur.Metadata.UID == uid {
			return d.cfg.Client.WaitGone(ctx, anchorPath(ns), poolGoneTimeout)
		}
	}
	return nil
}

// purgePool deletes the fixed-name bench objects in a pool namespace that
// are not owned by keepUID (the anchor just created) and, when runID is
// set, that carry its run label, then waits until they are gone. Objects
// without the bench label are someone else's and stay.
func (d *Driver) purgePool(ctx context.Context, ns, runID, keepUID string) error {
	var errs []error
	objs := poolObjects
	if ownServiceAccount(d.cfg.ServiceAccount) {
		// Only an account the bench made (bench label) goes; one an
		// administrator put in the pool namespace stays.
		objs = append(append(objs[:0:0], objs...), struct{ collection, name string }{"serviceaccounts", d.cfg.ServiceAccount})
	}
	for _, o := range objs {
		p := poolObjectPath(ns, o.collection, o.name)
		var obj struct {
			Metadata ObjectMeta `json:"metadata"`
		}
		err := d.cfg.Client.Get(ctx, p, &obj)
		if IsNotFound(err) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		l := obj.Metadata.Labels
		if l[LabelBench] != "1" || (runID != "" && l[LabelRun] != runID && l[LabelRun] != "") {
			continue
		}
		if keepUID != "" && ownedBy(obj.Metadata, keepUID) {
			continue
		}
		if err := d.cfg.Client.Delete(ctx, p); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := d.cfg.Client.WaitGone(ctx, p, poolGoneTimeout); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func ownedBy(m ObjectMeta, uid string) bool {
	for _, o := range m.OwnerReferences {
		if o.UID == uid {
			return true
		}
	}
	return false
}

// destroyPool releases the run's pool namespace: anchor first (owner
// garbage collection), then the run's own objects, waiting bounded until
// they are gone. It never deletes the namespace.
func (d *Driver) destroyPool(ctx context.Context, runID string) error {
	ns, ok, err := d.poolNamespaceOf(ctx, runID)
	if err != nil && !ok {
		return err
	}
	if !ok {
		return nil
	}
	var cur ConfigMap
	switch err := d.cfg.Client.Get(ctx, anchorPath(ns), &cur); {
	case IsNotFound(err):
		if err := d.purgePool(ctx, ns, runID, ""); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("anchor in %s: %w", ns, err)
	case anchorRun(cur) != runID:
		d.cfg.Logger.Warn("pool namespace is leased by another run now; leaving it", "namespace", ns, "run", runID)
	default:
		if err := d.releasePool(ctx, ns, runID, cur.Metadata.UID); err != nil {
			return fmt.Errorf("release %s: %w", ns, err)
		}
	}
	d.forget(runID)
	return nil
}

// ReleaseStaleAnchor removes a stale anchor found by `k8s cleanup` in pool
// mode, and the run objects it left, without touching the namespace.
func ReleaseStaleAnchor(ctx context.Context, c *Client, ns string, anchor ConfigMap) error {
	d := &Driver{cfg: Config{Client: c, Logger: schemas.NoopLogger{}}}
	return d.releasePool(ctx, ns, anchorRun(anchor), anchor.Metadata.UID)
}
