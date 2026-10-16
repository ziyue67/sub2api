package repository

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Cutover is restricted to a dedicated Kubernetes namespace of StatefulSets.
// Immutable controller UIDs + a common rollout epoch replace operator booleans.
// Namespace RBAC must forbid alternate controllers/force deletion during the
// cutover. A healthy API cannot prove a force-deleted process on a lost node dead.
type RegionalCutoverConfig struct {
	Endpoint  string
	CAFile    string
	TokenFile string
	Namespace string
	Workloads map[string]string // StatefulSet name -> immutable UID
}
type regionalObjectMeta struct {
	Name              string
	UID               string
	Generation        int64
	ResourceVersion   string
	DeletionTimestamp *time.Time
	Labels            map[string]string
	Annotations       map[string]string
	OwnerReferences   []struct {
		UID        string
		Kind       string
		Controller bool
	}
}
type regionalStatefulSet struct {
	Metadata regionalObjectMeta
	Spec     struct {
		Replicas int
		Template struct{ Metadata regionalObjectMeta }
	}
	Status struct {
		ObservedGeneration int64
		Replicas           int
		ReadyReplicas      int
		UpdatedReplicas    int
		CurrentRevision    string
		UpdateRevision     string
	}
}
type regionalPod struct {
	Metadata regionalObjectMeta
	Spec     struct{ NodeName string }
	Status   struct {
		Phase      string
		Conditions []struct {
			Type   string
			Status string
		}
	}
}
type regionalNode struct {
	Metadata regionalObjectMeta
	Status   struct {
		Conditions []struct {
			Type              string
			Status            string
			LastHeartbeatTime time.Time
		}
	}
}
type regionalInventoryClient struct {
	client    *http.Client
	endpoint  string
	tokenFile string
}

func newRegionalInventoryClient(c RegionalCutoverConfig) (*regionalInventoryClient, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || c.Namespace == "" || len(c.Workloads) == 0 {
		return nil, errors.New("invalid regional cutover inventory")
	}
	raw, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, errors.New("cutover CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("invalid cutover CA")
	}
	return &regionalInventoryClient{endpoint: c.Endpoint, tokenFile: c.TokenFile, client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("cutover redirects forbidden") }}}, nil
}
func (c *regionalInventoryClient) get(ctx context.Context, path string, v any) error {
	token, err := os.ReadFile(c.tokenFile)
	if err != nil || len(token) > 16384 {
		return errors.New("cutover token unavailable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	res, err := c.client.Do(req)
	if err != nil {
		return errors.New("cutover API unavailable")
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return errors.New("cutover API rejected inventory read")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 {
		return errors.New("cutover inventory exceeds limit")
	}
	if err = json.Unmarshal(raw, v); err != nil {
		return errors.New("invalid cutover inventory")
	}
	return nil
}
func verifyRegionalCutover(ctx context.Context, tx *sql.Tx, c RegionalConcurrencyRuntimeConfig) error {
	if c.Cutover == nil {
		return errors.New("live deployment inventory required for regional grants")
	}
	client, err := newRegionalInventoryClient(*c.Cutover)
	if err != nil {
		return err
	}
	defer client.client.CloseIdleConnections()
	return client.verify(ctx, tx, c)
}
func (c *regionalInventoryClient) verify(ctx context.Context, tx *sql.Tx, r RegionalConcurrencyRuntimeConfig) error {
	cfg := r.Cutover
	ns := url.PathEscape(cfg.Namespace)
	var workloads struct {
		Items    []regionalStatefulSet
		Metadata struct{ Continue string }
	}
	if err := c.get(ctx, "/apis/apps/v1/namespaces/"+ns+"/statefulsets", &workloads); err != nil {
		return err
	}
	if workloads.Metadata.Continue != "" || len(workloads.Items) != len(cfg.Workloads) {
		return errors.New("cutover workload inventory changed")
	}
	controllers := map[string]string{}
	revisions := map[string]string{}
	expected := map[string]bool{}
	for _, w := range workloads.Items {
		if cfg.Workloads[w.Metadata.Name] != w.Metadata.UID || w.Metadata.DeletionTimestamp != nil || w.Spec.Replicas <= 0 || w.Status.ObservedGeneration != w.Metadata.Generation || w.Status.ReadyReplicas != w.Spec.Replicas || w.Status.UpdatedReplicas != w.Spec.Replicas || w.Status.Replicas != w.Spec.Replicas || w.Status.CurrentRevision == "" || w.Status.CurrentRevision != w.Status.UpdateRevision || w.Spec.Template.Metadata.Annotations["sub2api.io/regional-epoch"] != r.Epoch {
			return errors.New("cutover rollout incomplete or identity changed")
		}
		controllers[w.Metadata.UID] = w.Metadata.Name
		revisions[w.Metadata.UID] = w.Status.UpdateRevision
	}
	// A dedicated namespace must contain no alternate workload controllers.
	for _, path := range []string{"/apis/apps/v1/namespaces/" + ns + "/deployments", "/apis/apps/v1/namespaces/" + ns + "/daemonsets", "/apis/batch/v1/namespaces/" + ns + "/jobs", "/apis/batch/v1/namespaces/" + ns + "/cronjobs"} {
		var list struct {
			Items    []json.RawMessage
			Metadata struct{ Continue string }
		}
		if err := c.get(ctx, path, &list); err != nil {
			return err
		}
		if len(list.Items) != 0 || list.Metadata.Continue != "" {
			return errors.New("cutover namespace contains uncontrolled workloads")
		}
	}
	var pods struct {
		Items    []regionalPod
		Metadata struct{ Continue string }
	}
	if err := c.get(ctx, "/api/v1/namespaces/"+ns+"/pods", &pods); err != nil {
		return err
	}
	if pods.Metadata.Continue != "" {
		return errors.New("incomplete cutover pod inventory")
	}
	nodes := map[string]bool{}
	fingerprint := []string{r.Epoch}
	for _, p := range pods.Items {
		owner := ""
		for _, o := range p.Metadata.OwnerReferences {
			if o.Controller && o.Kind == "StatefulSet" {
				owner = o.UID
			}
		}
		if controllers[owner] == "" || p.Metadata.DeletionTimestamp != nil || p.Metadata.UID == "" || p.Status.Phase != "Running" || p.Metadata.Labels["controller-revision-hash"] != revisions[owner] || p.Metadata.Annotations["sub2api.io/regional-epoch"] != r.Epoch {
			return errors.New("old or uncontrolled pod remains in cutover inventory")
		}
		ready := false
		for _, condition := range p.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
			}
		}
		if !ready || p.Spec.NodeName == "" {
			return errors.New("cutover pod not ready")
		}
		expected[p.Metadata.Name] = true
		nodes[p.Spec.NodeName] = true
		fingerprint = append(fingerprint, p.Metadata.UID+":"+owner+":"+revisions[owner])
	}
	if len(expected) != len(r.PrimaryInstances) {
		return errors.New("primary membership differs from live pods")
	}
	for _, id := range r.PrimaryInstances {
		if !expected[id] {
			return errors.New("expected primary pod missing")
		}
	}
	for name := range nodes {
		var node regionalNode
		if err := c.get(ctx, "/api/v1/nodes/"+url.PathEscape(name), &node); err != nil {
			return err
		}
		ready := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" && cond.Status == "True" && time.Since(cond.LastHeartbeatTime) < time.Minute {
				ready = true
			}
		}
		if !ready || node.Metadata.DeletionTimestamp != nil {
			return errors.New("cutover node health is uncertain")
		}
	}
	// Keep immutable evidence for this epoch. Pod replacement requires a new
	// audited epoch; a heartbeat cannot silently authorize changed membership.
	sort.Strings(fingerprint)
	raw, err := json.Marshal(fingerprint)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	result, err := tx.ExecContext(ctx, "INSERT INTO regional_cutover_evidence(epoch,inventory_sha256,payload,verified_at) VALUES($1,$2,$3,clock_timestamp()) ON CONFLICT(epoch) DO UPDATE SET verified_at=clock_timestamp() WHERE regional_cutover_evidence.inventory_sha256=EXCLUDED.inventory_sha256", r.Epoch, digest, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("cutover immutable pod inventory changed")
	}
	return nil
}
