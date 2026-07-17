// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestResourceCRUDInjectsClusterAndFiltersOtherClusters(t *testing.T) {
	foreign := object("BackupRepository", "foreign", map[string]any{"clusterRef": "other", "type": "Local"})
	client := newFakeClient(foreign)
	server, err := NewServer(client, "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/api/resources/repositories", nil)
	require.Equal(t, http.StatusOK, response.Code)
	var list unstructured.UnstructuredList
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Empty(t, list.Items)

	payload := object("BackupRepository", "local", map[string]any{
		"type": "Local", "local": map[string]any{"mode": "HostPath", "path": "/repository"},
	})
	response = request(t, server, http.MethodPost, "/api/resources/repositories", payload.Object)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var created unstructured.Unstructured
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created.Object))
	clusterRef, found, err := unstructured.NestedString(created.Object, "spec", "clusterRef")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "docker-desktop", clusterRef)
	require.Equal(t, "docker-desktop", created.GetLabels()["protection.platform.io/cluster"])

	created.Object["spec"].(map[string]any)["enabled"] = false
	response = request(t, server, http.MethodPut, "/api/resources/repositories/local", created.Object)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	enabled, found, err := unstructured.NestedBool(mustObject(t, response).Object, "spec", "enabled")
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, enabled)
}

func TestTaskCancelAction(t *testing.T) {
	task := object("BackupTask", "running", map[string]any{
		"clusterRef": "docker-desktop", "trigger": "Manual", "policyRef": map[string]any{"name": "policy"},
	})
	client := newFakeClient(task)
	server, err := NewServer(client, "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodPost, "/api/resources/backup-tasks/running/actions/cancel", nil)
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	updated := mustObject(t, response)
	cancelled, found, err := unstructured.NestedBool(updated.Object, "spec", "cancelRequested")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, cancelled)
}

func TestResourceQueryFiltersByKeywordPhaseAndType(t *testing.T) {
	readyLocal := object("BackupRepository", "local-primary", map[string]any{"clusterRef": "docker-desktop", "type": "Local"})
	readyLocal.Object["status"] = map[string]any{"phase": "Ready"}
	failedSFTP := object("BackupRepository", "sftp-dr", map[string]any{"clusterRef": "docker-desktop", "type": "SFTP"})
	failedSFTP.Object["status"] = map[string]any{"phase": "Failed", "message": "authentication failed"}
	client := newFakeClient(readyLocal, failedSFTP)
	server, err := NewServer(client, "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/api/resources/repositories?q=primary&phase=Ready&type=Local", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var list unstructured.UnstructuredList
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	require.Equal(t, "local-primary", list.Items[0].GetName())

	response = request(t, server, http.MethodGet, "/api/resources/repositories?q=authentication", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	require.Equal(t, "sftp-dr", list.Items[0].GetName())
}

func TestHealthAndEmbeddedUI(t *testing.T) {
	server, err := NewServer(newFakeClient(), "docker-desktop", "v1", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/api/health", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "docker-desktop")

	response = request(t, server, http.MethodGet, "/", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "备份与恢复控制台")
}

func TestCreateRejectsDifferentCluster(t *testing.T) {
	server, err := NewServer(newFakeClient(), "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	payload := object("BackupPolicy", "foreign", map[string]any{"clusterRef": "other", "selection": map[string]any{"mode": "Namespace", "includeNamespaces": []any{"default"}}, "repositoryRef": map[string]any{"name": "repo"}})
	response := request(t, server, http.MethodPost, "/api/resources/policies", payload.Object)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "docker-desktop")
}

func TestPolicyRunsAggregatesTasksAndRecoveryPoints(t *testing.T) {
	older := object("BackupTask", "run-old", map[string]any{
		"clusterRef": "docker-desktop", "policyRef": map[string]any{"name": "daily"},
	})
	older.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)))
	older.Object["status"] = map[string]any{"phase": "Completed"}
	newer := object("BackupTask", "run-new", map[string]any{
		"clusterRef": "docker-desktop", "policyRef": map[string]any{"name": "daily"},
	})
	newer.SetCreationTimestamp(metav1.NewTime(time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)))
	newer.Object["status"] = map[string]any{"phase": "Completed"}
	record := object("BackupRecord", "point-new", map[string]any{
		"clusterRef": "docker-desktop", "sourceTaskRef": map[string]any{"name": "run-new"},
		"policyRef": map[string]any{"name": "daily"},
	})
	record.Object["status"] = map[string]any{"phase": "Available"}
	client := newFakeClient(older, newer, record)
	server, err := NewServer(client, "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodGet, "/api/policy-runs/daily", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Policy string           `json:"policy"`
		Runs   []map[string]any `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, "daily", result.Policy)
	require.Len(t, result.Runs, 2)
	firstTask, ok := result.Runs[0]["task"].(map[string]any)
	require.True(t, ok)
	firstName, _, err := unstructured.NestedString(firstTask, "metadata", "name")
	require.NoError(t, err)
	require.Equal(t, "run-new", firstName)
	require.Contains(t, result.Runs[0], "record")
	require.Equal(t, "可恢复", result.Runs[0]["conclusion"])
}

func TestRunPolicyNowFreezesMergedSelection(t *testing.T) {
	repository := object("BackupRepository", "repo", map[string]any{
		"clusterRef": "docker-desktop", "type": "Local",
	})
	repository.SetUID(types.UID("repo-uid"))
	repository.SetGeneration(3)
	policy := object("BackupPolicy", "daily", map[string]any{
		"clusterRef": "docker-desktop",
		"selection": map[string]any{
			"mode": "Namespace", "includeNamespaces": []any{"payments"},
		},
		"repositoryRef": map[string]any{"name": "repo"},
		"timeout":       "2h",
		"retryPolicy":   map[string]any{"maxAttempts": int64(2)},
	})
	policy.SetUID(types.UID("policy-uid"))
	policy.SetGeneration(7)
	client := newFakeClient(repository, policy)
	server, err := NewServer(client, "docker-desktop", "test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	response := request(t, server, http.MethodPost, "/api/resources/policies/daily/actions/run", nil)
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	task := mustObject(t, response)
	policyName, _, err := unstructured.NestedString(task.Object, "spec", "policyRef", "name")
	require.NoError(t, err)
	require.Equal(t, "daily", policyName)
	selectionMode, _, err := unstructured.NestedString(task.Object, "spec", "selectionSnapshot", "mode")
	require.NoError(t, err)
	require.Equal(t, "Namespace", selectionMode)
	require.Equal(t, float64(7), task.Object["spec"].(map[string]any)["policyGeneration"])
}

func newFakeClient(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	listKinds := map[schema.GroupVersionResource]string{}
	for _, descriptor := range resources {
		listKinds[descriptor.GVR] = descriptor.ListKind
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objects...)
}

func object(kind, name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "protection.platform.io/v1alpha1", "kind": kind,
		"metadata": map[string]any{"name": name, "creationTimestamp": metav1.Now().Format(time.RFC3339)},
		"spec":     spec,
	}}
}

func request(t *testing.T, handler http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		body = bytes.NewReader(data)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, body))
	return recorder
}

func mustObject(t *testing.T, response *httptest.ResponseRecorder) *unstructured.Unstructured {
	t.Helper()
	object := &unstructured.Unstructured{}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &object.Object))
	return object
}
