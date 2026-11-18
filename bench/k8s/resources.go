package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Resource paths of the API objects the driver uses.
func nsPath(ns string) string { return "/api/v1/namespaces/" + url.PathEscape(ns) }

func corePath(ns, kind string) string { return nsPath(ns) + "/" + kind }

func netpolPath(ns string) string {
	return "/apis/networking.k8s.io/v1/namespaces/" + url.PathEscape(ns) + "/networkpolicies"
}

func deployPath(ns string) string {
	return "/apis/apps/v1/namespaces/" + url.PathEscape(ns) + "/deployments"
}

// Create posts obj to the collection path and decodes the created object
// into out (when not nil).
func (c *Client) Create(ctx context.Context, collection string, obj, out any) error {
	return c.Do(ctx, http.MethodPost, collection, nil, obj, out)
}

// Apply creates obj, or replaces the existing object of the same name with
// a merge patch of obj when it exists already (gateway objects).
func (c *Client) Apply(ctx context.Context, collection, name string, obj any) error {
	err := c.Create(ctx, collection, obj, nil)
	if !IsConflict(err) {
		return err
	}
	b, merr := jsonMarshal(obj)
	if merr != nil {
		return merr
	}
	return c.Do(ctx, http.MethodPatch, collection+"/"+url.PathEscape(name), nil, mergePatch(b), nil)
}

// Delete deletes one object with background propagation; a missing object
// is not an error.
func (c *Client) Delete(ctx context.Context, path string) error {
	body := map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Background"}
	err := c.Do(ctx, http.MethodDelete, path, nil, body, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Get decodes one object; a 404 is an error the caller tests with
// IsNotFound.
func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.Do(ctx, http.MethodGet, path, nil, nil, out)
}

// WaitGone polls path until the API server answers 404 or timeout passes.
func (c *Client) WaitGone(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var obj map[string]any
		err := c.Get(ctx, path, &obj)
		if IsNotFound(err) {
			return nil
		}
		if err != nil && !isTransient(err) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still exists after %s", path, timeout)
		}
		if err := sleepCtx(ctx, 500*time.Millisecond); err != nil {
			return err
		}
	}
}

// Events returns the namespace's events, oldest first, as one line each.
func (c *Client) Events(ctx context.Context, ns string) ([]Event, error) {
	var list struct {
		Items []Event `json:"items"`
	}
	if err := c.Get(ctx, corePath(ns, "events"), &list); err != nil {
		return nil, err
	}
	sort.SliceStable(list.Items, func(i, j int) bool { return list.Items[i].LastTimestamp < list.Items[j].LastTimestamp })
	return list.Items, nil
}

// eventLines formats events for an error message or kubernetes.json.
func eventLines(evs []Event) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, fmt.Sprintf("%s %s %s/%s: %s", e.Type, e.Reason, strings.ToLower(e.InvolvedObject.Kind), e.InvolvedObject.Name, clip(e.Message, 300)))
	}
	return out
}

// Version is the API server's /version answer.
type Version struct {
	Major      string `json:"major"`
	Minor      string `json:"minor"`
	GitVersion string `json:"gitVersion"`
}

// ServerVersion reads /version.
func (c *Client) ServerVersion(ctx context.Context) (*Version, error) {
	var v Version
	if err := c.Get(ctx, "/version", &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// CanI asks the API server whether this identity may do verb on resource
// (group "" for the core group) in ns, through a SelfSubjectAccessReview.
func (c *Client) CanI(ctx context.Context, verb, group, resource, subresource, ns string) (bool, string, error) {
	review := map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": map[string]any{
			"verb": verb, "group": group, "resource": resource, "subresource": subresource, "namespace": ns}},
	}
	var out struct {
		Status struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reason"`
		} `json:"status"`
	}
	if err := c.Create(ctx, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", review, &out); err != nil {
		return false, "", err
	}
	return out.Status.Allowed, out.Status.Reason, nil
}

func isTransient(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == http.StatusTooManyRequests || ae.Code >= 500
	}
	return true // a transport error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

var jsonMarshal = json.Marshal
