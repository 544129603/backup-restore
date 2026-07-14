// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/collector"
	"github.com/example/backup-restore-operator/internal/controller"
	"github.com/example/backup-restore-operator/internal/metrics"
	"github.com/example/backup-restore-operator/internal/snapshot"
)

var (
	scheme  = runtime.NewScheme()
	version = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(protectionv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddress, probeAddress, workspace, webhookCertDir, clusterRef string
	var leaderElection, enableHTTP2, enableWebhooks bool
	var maxConcurrentBackups, maxConcurrentRestores int
	flag.StringVar(&metricsAddress, "metrics-bind-address", ":8080", "Address for the metrics endpoint; use 0 to disable.")
	flag.StringVar(&probeAddress, "health-probe-bind-address", ":8081", "Address for health probes.")
	flag.StringVar(&workspace, "workspace", "/workspace", "Task staging workspace.")
	flag.StringVar(&clusterRef, "cluster-ref", os.Getenv("CLUSTER_REF"), "Logical identity of the cluster this Operator is allowed to execute against.")
	flag.BoolVar(&leaderElection, "leader-elect", true, "Use Kubernetes Lease leader election.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for metrics and webhooks.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", true, "Register admission webhooks.")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs", "Webhook TLS certificate directory.")
	flag.IntVar(&maxConcurrentBackups, "max-concurrent-backups", 3, "Maximum concurrent BackupTask reconciles.")
	flag.IntVar(&maxConcurrentRestores, "max-concurrent-restores", 1, "Maximum concurrent RestoreTask reconciles.")
	zapOptions := zap.Options{Development: false}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))

	_ = enableHTTP2 // controller-runtime defaults to HTTP/1.1; retained as a stable CLI contract.
	config := ctrl.GetConfigOrDie()
	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress: probeAddress,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "backup-restore-operator.protection.platform.io",
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443, CertDir: webhookCertDir}),
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to create manager")
		os.Exit(1)
	}
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		ctrl.Log.Error(err, "unable to create dynamic client")
		os.Exit(1)
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		ctrl.Log.Error(err, "unable to create discovery client")
		os.Exit(1)
	}
	resolver := &collector.Resolver{Discovery: discoveryClient, Dynamic: dynamicClient, PageSize: 500}
	resourceCollector := &collector.Collector{Dynamic: dynamicClient, PageSize: 500}
	snapshotManager := &snapshot.Manager{Client: manager.GetClient(), Dynamic: dynamicClient}
	recorder := manager.GetEventRecorderFor("backup-restore-operator")

	reconcilers := []interface{ SetupWithManager(ctrl.Manager) error }{
		&controller.RepositoryReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Recorder: recorder, ClusterRef: clusterRef},
		&controller.ScopeReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Resolver: resolver, Snapshots: snapshotManager, ClusterRef: clusterRef},
		&controller.PolicyReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), ClusterRef: clusterRef},
		&controller.BackupTaskReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Recorder: recorder, Resolver: resolver, Collector: resourceCollector, Snapshots: snapshotManager, Workspace: workspace, Version: version, ClusterRef: clusterRef, MaxConcurrent: maxConcurrentBackups},
		&controller.BackupRecordReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Snapshots: snapshotManager, ClusterRef: clusterRef},
		&controller.RestoreTaskReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Dynamic: dynamicClient, Mapper: manager.GetRESTMapper(), Snapshots: snapshotManager, Workspace: workspace, ClusterRef: clusterRef, MaxConcurrent: maxConcurrentRestores},
		&controller.RetentionReconciler{Client: manager.GetClient(), ClusterRef: clusterRef},
		&controller.BackupPluginConfigReconciler{Client: manager.GetClient(), Scheme: manager.GetScheme(), Version: version},
		&controller.GarbageCollectionReconciler{Client: manager.GetClient(), Workspace: workspace, ClusterRef: clusterRef},
	}
	for _, reconciler := range reconcilers {
		if err = reconciler.SetupWithManager(manager); err != nil {
			ctrl.Log.Error(err, "unable to register controller")
			os.Exit(1)
		}
	}
	if enableWebhooks {
		if err = protectionv1alpha1.SetupWebhooksWithManager(manager); err != nil {
			ctrl.Log.Error(err, "unable to register admission webhooks")
			os.Exit(1)
		}
	}
	metrics.Register()
	if err = manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to add health check")
		os.Exit(1)
	}
	if err = manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		ctrl.Log.Error(err, "unable to add readiness check")
		os.Exit(1)
	}
	if clusterRef == "" {
		ctrl.Log.Info("cluster-ref is empty; development mode will reconcile every clusterRef")
	}
	ctrl.Log.Info("starting backup and restore operator", "version", version, "clusterRef", clusterRef)
	if err = manager.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "manager exited")
		os.Exit(1)
	}
}
