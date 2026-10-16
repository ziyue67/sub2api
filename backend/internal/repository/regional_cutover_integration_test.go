//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegionalAdmissionLiveInventoryFencesOldAndReplacedPods(t *testing.T) {
	epoch := fmt.Sprint(time.Now().UnixNano())
	revision := "new"
	uid := "pod-uid"
	old := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var v any = map[string]any{"items": []any{}}
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/primary/statefulsets":
			v = map[string]any{"items": []any{map[string]any{"metadata": map[string]any{"name": "primary", "uid": "owner", "generation": 1}, "spec": map[string]any{"replicas": 1, "template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{"sub2api.io/regional-epoch": epoch}}}}, "status": map[string]any{"observedGeneration": 1, "replicas": 1, "readyReplicas": 1, "updatedReplicas": 1, "currentRevision": "new", "updateRevision": "new"}}}}
		case "/api/v1/namespaces/primary/pods":
			items := []any{map[string]any{"metadata": map[string]any{"name": "primary-0", "uid": uid, "labels": map[string]string{"controller-revision-hash": revision}, "annotations": map[string]string{"sub2api.io/regional-epoch": epoch}, "ownerReferences": []any{map[string]any{"uid": "owner", "kind": "StatefulSet", "controller": true}}}, "spec": map[string]any{"nodeName": "node"}, "status": map[string]any{"phase": "Running", "conditions": []any{map[string]string{"type": "Ready", "status": "True"}}}}}
			if old {
				items = append(items, map[string]any{"metadata": map[string]string{"name": "old"}})
			}
			v = map[string]any{"items": items}
		case "/api/v1/nodes/node":
			v = map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True", "lastHeartbeatTime": time.Now().UTC()}}}}
		}
		_ = json.NewEncoder(w).Encode(v)
	}))
	defer server.Close()
	token := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(token, []byte("mock"), 0600))
	client := &regionalInventoryClient{client: server.Client(), endpoint: server.URL, tokenFile: token}
	cfg := RegionalConcurrencyRuntimeConfig{Epoch: epoch, PrimaryInstances: []string{"primary-0"}, Cutover: &RegionalCutoverConfig{Namespace: "primary", Workloads: map[string]string{"primary": "owner"}}}
	check := func() error {
		tx, err := integrationDB.BeginTx(context.Background(), nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err = client.verify(context.Background(), tx, cfg); err != nil {
			return err
		}
		return tx.Commit()
	}
	require.NoError(t, check())
	old = true
	require.Error(t, check())
	old = false
	revision = "old"
	require.Error(t, check())
	revision = "new"
	uid = "replacement"
	require.Error(t, check())
}
