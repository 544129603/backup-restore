// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package webui

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
)

const (
	apiPrefix            = "/api/resources/"
	annotationRefreshAt  = "protection.platform.io/ui-refresh-requested-at"
	annotationUIRequest  = "protection.platform.io/ui-request-id"
	maximumRequestBytes  = 2 << 20
	defaultServerVersion = "dev"
)

type resourceDescriptor struct {
	GVR        schema.GroupVersionResource
	Kind       string
	ListKind   string
	ClusterRef bool
}

var resources = map[string]resourceDescriptor{
	"repositories": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "backuprepositories"},
		Kind: "BackupRepository", ListKind: "BackupRepositoryList", ClusterRef: true,
	},
	"policies": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "backuppolicies"},
		Kind: "BackupPolicy", ListKind: "BackupPolicyList", ClusterRef: true,
	},
	"backup-tasks": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "backuptasks"},
		Kind: "BackupTask", ListKind: "BackupTaskList", ClusterRef: true,
	},
	"records": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "backuprecords"},
		Kind: "BackupRecord", ListKind: "BackupRecordList", ClusterRef: true,
	},
	"restore-tasks": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "restoretasks"},
		Kind: "RestoreTask", ListKind: "RestoreTaskList", ClusterRef: true,
	},
	"configs": {
		GVR:  schema.GroupVersionResource{Group: protectionv1alpha1.GroupVersion.Group, Version: protectionv1alpha1.GroupVersion.Version, Resource: "backuppluginconfigs"},
		Kind: "BackupPluginConfig", ListKind: "BackupPluginConfigList",
	},
}

//go:embed static/*
var staticFiles embed.FS

type Server struct {
	client     dynamic.Interface
	clusterRef string
	version    string
	logger     *slog.Logger
	handler    http.Handler
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewServer(client dynamic.Interface, clusterRef, version string, logger *slog.Logger) (*Server, error) {
	if client == nil {
		return nil, errors.New("dynamic Kubernetes client is required")
	}
	if version == "" {
		version = defaultServerVersion
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{client: client, clusterRef: clusterRef, version: version, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/policy-runs/{name}", s.handlePolicyRuns)
	mux.HandleFunc(apiPrefix, s.handleResources)

	staticRoot, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil, fmt.Errorf("open embedded UI: %w", err)
	}
	mux.Handle("/", spaHandler{root: staticRoot, files: http.FileServer(http.FS(staticRoot))})
	s.handler = s.withMiddleware(mux)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		next.ServeHTTP(w, r)
		s.logger.Debug("web request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "clusterRef": s.clusterRef, "version": s.version, "time": time.Now().UTC(),
	})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	type summary struct {
		Total  int            `json:"total"`
		Phases map[string]int `json:"phases"`
	}
	response := struct {
		ClusterRef string             `json:"clusterRef"`
		Resources  map[string]summary `json:"resources"`
		Recent     []map[string]any   `json:"recentTasks"`
	}{ClusterRef: s.clusterRef, Resources: map[string]summary{}}

	keys := make([]string, 0, len(resources))
	for key := range resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		descriptor := resources[key]
		list, err := s.client.Resource(descriptor.GVR).List(r.Context(), metav1.ListOptions{})
		if err != nil {
			writeKubernetesError(w, err)
			return
		}
		items := s.filterCluster(descriptor, list.Items)
		itemSummary := summary{Total: len(items), Phases: map[string]int{}}
		for i := range items {
			phase, _, _ := unstructured.NestedString(items[i].Object, "status", "phase")
			if phase == "" {
				phase = "Unknown"
			}
			itemSummary.Phases[phase]++
			if key == "backup-tasks" || key == "restore-tasks" {
				response.Recent = append(response.Recent, compactTask(key, &items[i]))
			}
		}
		response.Resources[key] = itemSummary
	}
	sort.Slice(response.Recent, func(i, j int) bool {
		return fmt.Sprint(response.Recent[i]["createdAt"]) > fmt.Sprint(response.Recent[j]["createdAt"])
	})
	if len(response.Recent) > 8 {
		response.Recent = response.Recent[:8]
	}
	writeJSON(w, http.StatusOK, response)
}

func compactTask(resource string, object *unstructured.Unstructured) map[string]any {
	phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
	step, _, _ := unstructured.NestedString(object.Object, "status", "step")
	percent, _, _ := unstructured.NestedInt64(object.Object, "status", "progress", "percent")
	if resource == "restore-tasks" && percent == 0 {
		total, _, _ := unstructured.NestedInt64(object.Object, "status", "progress", "total")
		processed, _, _ := unstructured.NestedInt64(object.Object, "status", "progress", "processed")
		if total > 0 {
			percent = processed * 100 / total
		}
	}
	return map[string]any{
		"resource": resource, "name": object.GetName(), "phase": phase, "step": step,
		"percent": percent, "createdAt": object.GetCreationTimestamp(),
	}
}

func (s *Server) handlePolicyRuns(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_NAME", "策略名称不能为空")
		return
	}
	tasks, err := s.client.Resource(resources["backup-tasks"].GVR).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		writeKubernetesError(w, err)
		return
	}
	records, err := s.client.Resource(resources["records"].GVR).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		writeKubernetesError(w, err)
		return
	}
	recordByTask := map[string]unstructured.Unstructured{}
	for _, record := range s.filterCluster(resources["records"], records.Items) {
		taskName, _, _ := unstructured.NestedString(record.Object, "spec", "sourceTaskRef", "name")
		if taskName != "" {
			recordByTask[taskName] = record
		}
	}
	runs := make([]map[string]any, 0)
	for _, task := range s.filterCluster(resources["backup-tasks"], tasks.Items) {
		policyName, _, _ := unstructured.NestedString(task.Object, "spec", "policyRef", "name")
		if policyName != name {
			continue
		}
		run := map[string]any{"task": task.Object, "conclusion": backupConclusion(&task, nil)}
		if record, ok := recordByTask[task.GetName()]; ok {
			recordCopy := record
			run["record"] = record.Object
			run["conclusion"] = backupConclusion(&task, &recordCopy)
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		left, _ := runs[i]["task"].(map[string]any)
		right, _ := runs[j]["task"].(map[string]any)
		leftCreatedAt, _, _ := unstructured.NestedString(left, "metadata", "creationTimestamp")
		rightCreatedAt, _, _ := unstructured.NestedString(right, "metadata", "creationTimestamp")
		return leftCreatedAt > rightCreatedAt
	})
	writeJSON(w, http.StatusOK, map[string]any{"policy": name, "runs": runs})
}

func backupConclusion(task, record *unstructured.Unstructured) string {
	taskPhase, _, _ := unstructured.NestedString(task.Object, "status", "phase")
	if record == nil {
		switch taskPhase {
		case protectionv1alpha1.BackupPhaseFailed, protectionv1alpha1.BackupPhaseCancelled:
			return "未生成恢复点"
		case protectionv1alpha1.BackupPhaseCompleted, protectionv1alpha1.BackupPhasePartiallyFailed:
			return "等待生成恢复点"
		default:
			return "正在备份"
		}
	}
	recordPhase, _, _ := unstructured.NestedString(record.Object, "status", "phase")
	switch recordPhase {
	case protectionv1alpha1.RecordPhaseAvailable:
		return "可恢复"
	case protectionv1alpha1.RecordPhasePartiallyAvailable:
		return "有限恢复"
	case protectionv1alpha1.RecordPhaseVerifying, protectionv1alpha1.RecordPhasePending:
		return "恢复点校验中"
	case protectionv1alpha1.RecordPhaseBroken:
		return "恢复点已损坏"
	case protectionv1alpha1.RecordPhaseRepoUnavailable:
		return "恢复点暂不可访问"
	default:
		return recordPhase
	}
}

func (s *Server) handleResources(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, apiPrefix), "/")
	parts := strings.Split(path, "/")
	if path == "" || len(parts) > 4 {
		writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "API 路径不存在")
		return
	}
	resourceKey, err := url.PathUnescape(parts[0])
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_PATH", "资源路径无效")
		return
	}
	descriptor, ok := resources[resourceKey]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "不支持该资源类型")
		return
	}

	if len(parts) == 1 {
		s.handleCollection(w, r, descriptor)
		return
	}
	name, err := url.PathUnescape(parts[1])
	if err != nil || name == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_NAME", "对象名称无效")
		return
	}
	if len(parts) == 2 {
		s.handleObject(w, r, resourceKey, descriptor, name)
		return
	}
	if len(parts) == 4 && parts[2] == "actions" && r.Method == http.MethodPost {
		action, actionErr := url.PathUnescape(parts[3])
		if actionErr != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_ACTION", "操作名称无效")
			return
		}
		s.handleAction(w, r, resourceKey, descriptor, name, action)
		return
	}
	writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "API 路径不存在")
}

func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request, descriptor resourceDescriptor) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.client.Resource(descriptor.GVR).List(r.Context(), metav1.ListOptions{})
		if err != nil {
			writeKubernetesError(w, err)
			return
		}
		list.Items = filterQuery(s.filterCluster(descriptor, list.Items), r.URL.Query())
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		object, err := readObject(r.Body)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
		if err = s.prepareCreate(object, descriptor); err != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_OBJECT", err.Error())
			return
		}
		created, err := s.client.Resource(descriptor.GVR).Create(r.Context(), object, metav1.CreateOptions{})
		if err != nil {
			writeKubernetesError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "不支持该请求方法")
	}
}

func filterQuery(items []unstructured.Unstructured, query url.Values) []unstructured.Unstructured {
	keyword := strings.ToLower(strings.TrimSpace(query.Get("q")))
	phaseFilter := strings.ToLower(strings.TrimSpace(query.Get("phase")))
	typeFilter := strings.ToLower(strings.TrimSpace(query.Get("type")))
	limit := len(items)
	if rawLimit := query.Get("limit"); rawLimit != "" {
		if parsed, err := strconv.Atoi(rawLimit); err == nil && parsed > 0 && parsed < limit {
			limit = parsed
		}
	}
	filtered := make([]unstructured.Unstructured, 0, min(len(items), limit))
	for i := range items {
		item := &items[i]
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		if phaseFilter != "" && strings.ToLower(phase) != phaseFilter {
			continue
		}
		if typeFilter != "" {
			matched := false
			for _, field := range []string{"type", "mode", "trigger"} {
				value, _, _ := unstructured.NestedString(item.Object, "spec", field)
				if strings.ToLower(value) == typeFilter {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		if keyword != "" {
			spec, _, _ := unstructured.NestedMap(item.Object, "spec")
			specJSON, _ := json.Marshal(spec)
			haystack := strings.ToLower(strings.Join([]string{
				item.GetName(), phase, fmt.Sprint(item.Object["status"]), string(specJSON),
			}, " "))
			if !strings.Contains(haystack, keyword) {
				continue
			}
		}
		filtered = append(filtered, items[i])
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, resourceKey string, descriptor resourceDescriptor, name string) {
	resourceClient := s.client.Resource(descriptor.GVR)
	switch r.Method {
	case http.MethodGet:
		object, err := resourceClient.Get(r.Context(), name, metav1.GetOptions{})
		if err != nil {
			writeKubernetesError(w, err)
			return
		}
		if !s.allowedCluster(descriptor, object) {
			writeAPIError(w, http.StatusNotFound, "NOT_FOUND", "对象不存在于当前集群视图")
			return
		}
		writeJSON(w, http.StatusOK, object)
	case http.MethodPut:
		desired, err := readObject(r.Body)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
			return
		}
		updated, err := s.updateObject(r.Context(), resourceClient, descriptor, name, desired)
		if err != nil {
			writeKubernetesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := s.deleteObject(r.Context(), resourceClient, resourceKey, name, r.URL.Query()); err != nil {
			writeKubernetesError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"name": name, "deletionRequested": true})
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "不支持该请求方法")
	}
}

func (s *Server) prepareCreate(object *unstructured.Unstructured, descriptor resourceDescriptor) error {
	if object.GetName() == "" {
		return errors.New("metadata.name 不能为空")
	}
	object.SetAPIVersion(protectionv1alpha1.GroupVersion.String())
	object.SetKind(descriptor.Kind)
	object.SetNamespace("")
	object.SetResourceVersion("")
	object.SetUID("")
	object.SetGeneration(0)
	object.SetManagedFields(nil)
	object.SetCreationTimestamp(metav1.Time{})
	delete(object.Object, "status")
	if _, found := object.Object["spec"]; !found {
		return errors.New("spec 不能为空")
	}
	return s.enforceCluster(descriptor, object)
}

func (s *Server) updateObject(ctx context.Context, resourceClient dynamic.ResourceInterface, descriptor resourceDescriptor, name string, desired *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	current, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !s.allowedCluster(descriptor, current) {
		return nil, apierrors.NewNotFound(descriptor.GVR.GroupResource(), name)
	}
	if desired.GetName() != "" && desired.GetName() != name {
		return nil, apierrors.NewBadRequest("metadata.name 不允许修改")
	}
	spec, found, err := unstructured.NestedMap(desired.Object, "spec")
	if err != nil || !found {
		return nil, apierrors.NewBadRequest("spec 不能为空")
	}
	updated := current.DeepCopy()
	if err = unstructured.SetNestedMap(updated.Object, spec, "spec"); err != nil {
		return nil, err
	}
	if desired.GetLabels() != nil {
		updated.SetLabels(desired.GetLabels())
	}
	if desired.GetAnnotations() != nil {
		updated.SetAnnotations(desired.GetAnnotations())
	}
	if err = s.enforceCluster(descriptor, updated); err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	return resourceClient.Update(ctx, updated, metav1.UpdateOptions{})
}

func (s *Server) deleteObject(ctx context.Context, resourceClient dynamic.ResourceInterface, resourceKey, name string, query url.Values) error {
	if resourceKey == "records" {
		mode := query.Get("mode")
		switch mode {
		case protectionv1alpha1.DeleteModeRecordOnly, protectionv1alpha1.DeleteModeRepositoryData, protectionv1alpha1.DeleteModeRepositoryDataAndSnapshots:
		default:
			return apierrors.NewBadRequest("删除备份记录必须选择有效的 mode")
		}
		object, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		annotations := object.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[protectionv1alpha1.AnnotationDeleteConfirmed] = "true"
		annotations[protectionv1alpha1.AnnotationDeleteMode] = mode
		if query.Get("force") == "true" {
			annotations[protectionv1alpha1.AnnotationForceDel] = "true"
		}
		object.SetAnnotations(annotations)
		if _, err = resourceClient.Update(ctx, object, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	if resourceKey == "repositories" && query.Get("force") == "true" {
		object, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		annotations := object.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[protectionv1alpha1.AnnotationForceDel] = "true"
		object.SetAnnotations(annotations)
		if _, err = resourceClient.Update(ctx, object, metav1.UpdateOptions{}); err != nil {
			return err
		}
	}
	return resourceClient.Delete(ctx, name, metav1.DeleteOptions{})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request, resourceKey string, descriptor resourceDescriptor, name, action string) {
	var (
		object *unstructured.Unstructured
		err    error
	)
	switch {
	case resourceKey == "policies" && action == "run":
		object, err = s.runPolicyNow(r.Context(), name)
	case resourceKey == "policies" && (action == "suspend" || action == "resume"):
		object, err = s.setPolicySuspended(r.Context(), descriptor, name, action == "suspend")
	case (resourceKey == "backup-tasks" || resourceKey == "restore-tasks") && action == "cancel":
		object, err = s.cancelTask(r.Context(), descriptor, name)
	case (resourceKey == "repositories" || resourceKey == "records") && (action == "refresh" || action == "verify"):
		object, err = s.markForRefresh(r.Context(), descriptor, name)
	default:
		writeAPIError(w, http.StatusBadRequest, "ACTION_NOT_SUPPORTED", "当前对象不支持该操作")
		return
	}
	if err != nil {
		writeKubernetesError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, object)
}

func (s *Server) runPolicyNow(ctx context.Context, name string) (*unstructured.Unstructured, error) {
	policyDescriptor := resources["policies"]
	policy, err := s.client.Resource(policyDescriptor.GVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !s.allowedCluster(policyDescriptor, policy) {
		return nil, apierrors.NewNotFound(policyDescriptor.GVR.GroupResource(), name)
	}
	repositoryName, _, _ := unstructured.NestedString(policy.Object, "spec", "repositoryRef", "name")
	if repositoryName == "" {
		return nil, apierrors.NewBadRequest("策略缺少 repositoryRef")
	}
	repository, err := s.client.Resource(resources["repositories"].GVR).Get(ctx, repositoryName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	policySpec, _, _ := unstructured.NestedMap(policy.Object, "spec")
	selection, _, _ := unstructured.NestedMap(policy.Object, "spec", "selection")
	requestID := randomID()
	suffix := "-manual-" + strings.ToLower(requestID[:8])
	policyPrefix := name
	if maximumPrefix := 63 - len(suffix); len(policyPrefix) > maximumPrefix {
		policyPrefix = strings.TrimRight(policyPrefix[:maximumPrefix], "-")
	}
	taskName := policyPrefix + suffix
	task := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": protectionv1alpha1.GroupVersion.String(),
		"kind":       resources["backup-tasks"].Kind,
		"metadata": map[string]any{
			"name": taskName,
			"labels": map[string]any{
				protectionv1alpha1.LabelCluster:   s.clusterRef,
				protectionv1alpha1.LabelTrigger:   protectionv1alpha1.BackupTriggerManual,
				protectionv1alpha1.LabelPolicyUID: string(policy.GetUID()),
			},
			"annotations": map[string]any{annotationUIRequest: requestID},
		},
		"spec": map[string]any{
			"clusterRef":           s.clusterRef,
			"trigger":              protectionv1alpha1.BackupTriggerManual,
			"policyRef":            map[string]any{"name": policy.GetName(), "uid": string(policy.GetUID())},
			"repositoryRef":        map[string]any{"name": repositoryName, "uid": string(repository.GetUID())},
			"selectionSnapshot":    selection,
			"policyGeneration":     policy.GetGeneration(),
			"repositoryGeneration": repository.GetGeneration(),
			"timeout":              policySpec["timeout"],
			"retryPolicy":          policySpec["retryPolicy"],
			"failurePolicy":        "Continue",
			"allowPartialRecord":   true,
			"idempotencyKey":       "webui/" + requestID,
		},
	}}
	return s.client.Resource(resources["backup-tasks"].GVR).Create(ctx, task, metav1.CreateOptions{})
}

func (s *Server) setPolicySuspended(ctx context.Context, descriptor resourceDescriptor, name string, suspended bool) (*unstructured.Unstructured, error) {
	client := s.client.Resource(descriptor.GVR)
	object, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if err = unstructured.SetNestedField(object.Object, suspended, "spec", "suspend"); err != nil {
		return nil, err
	}
	if !suspended {
		_ = unstructured.SetNestedField(object.Object, true, "spec", "enabled")
	}
	return client.Update(ctx, object, metav1.UpdateOptions{})
}

func (s *Server) cancelTask(ctx context.Context, descriptor resourceDescriptor, name string) (*unstructured.Unstructured, error) {
	client := s.client.Resource(descriptor.GVR)
	object, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if err = unstructured.SetNestedField(object.Object, true, "spec", "cancelRequested"); err != nil {
		return nil, err
	}
	_ = unstructured.SetNestedField(object.Object, "用户通过管理页面取消", "spec", "cancelReason")
	return client.Update(ctx, object, metav1.UpdateOptions{})
}

func (s *Server) markForRefresh(ctx context.Context, descriptor resourceDescriptor, name string) (*unstructured.Unstructured, error) {
	client := s.client.Resource(descriptor.GVR)
	object, err := client.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[annotationRefreshAt] = time.Now().UTC().Format(time.RFC3339Nano)
	object.SetAnnotations(annotations)
	return client.Update(ctx, object, metav1.UpdateOptions{})
}

func (s *Server) enforceCluster(descriptor resourceDescriptor, object *unstructured.Unstructured) error {
	if !descriptor.ClusterRef {
		return nil
	}
	clusterRef, _, _ := unstructured.NestedString(object.Object, "spec", "clusterRef")
	if clusterRef == "" {
		if s.clusterRef == "" {
			return errors.New("spec.clusterRef 不能为空")
		}
		if err := unstructured.SetNestedField(object.Object, s.clusterRef, "spec", "clusterRef"); err != nil {
			return err
		}
		clusterRef = s.clusterRef
	}
	if s.clusterRef != "" && clusterRef != s.clusterRef {
		return fmt.Errorf("spec.clusterRef 必须为 %q", s.clusterRef)
	}
	labels := object.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[protectionv1alpha1.LabelCluster] = clusterRef
	object.SetLabels(labels)
	return nil
}

func (s *Server) allowedCluster(descriptor resourceDescriptor, object *unstructured.Unstructured) bool {
	if !descriptor.ClusterRef || s.clusterRef == "" {
		return true
	}
	clusterRef, _, _ := unstructured.NestedString(object.Object, "spec", "clusterRef")
	return clusterRef == s.clusterRef
}

func (s *Server) filterCluster(descriptor resourceDescriptor, items []unstructured.Unstructured) []unstructured.Unstructured {
	if !descriptor.ClusterRef || s.clusterRef == "" {
		return items
	}
	filtered := make([]unstructured.Unstructured, 0, len(items))
	for i := range items {
		if s.allowedCluster(descriptor, &items[i]) {
			filtered = append(filtered, items[i])
		}
	}
	return filtered
}

func readObject(reader io.Reader) (*unstructured.Unstructured, error) {
	limited := io.LimitReader(reader, maximumRequestBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(payload) > maximumRequestBytes {
		return nil, fmt.Errorf("请求体不能超过 %d 字节", maximumRequestBytes)
	}
	object := &unstructured.Unstructured{}
	if err = json.Unmarshal(payload, &object.Object); err != nil {
		return nil, fmt.Errorf("JSON 格式错误: %w", err)
	}
	return object, nil
}

func randomID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func writeKubernetesError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "KUBERNETES_ERROR"
	switch {
	case apierrors.IsNotFound(err):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case apierrors.IsAlreadyExists(err):
		status, code = http.StatusConflict, "ALREADY_EXISTS"
	case apierrors.IsConflict(err):
		status, code = http.StatusConflict, "RESOURCE_CONFLICT"
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		status, code = http.StatusBadRequest, "INVALID_OBJECT"
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		status, code = http.StatusForbidden, "PERMISSION_DENIED"
	}
	writeAPIError(w, status, code, err.Error())
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Error: apiErrorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type spaHandler struct {
	root  fs.FS
	files http.Handler
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(h.root, path); err != nil {
		r.URL.Path = "/"
	}
	h.files.ServeHTTP(w, r)
}
