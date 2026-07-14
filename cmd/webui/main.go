// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"k8s.io/client-go/dynamic"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/example/backup-restore-operator/internal/webui"
)

var version = "dev"

func main() {
	var address, clusterRef string
	flag.StringVar(&address, "bind-address", ":8082", "HTTP listen address.")
	flag.StringVar(&clusterRef, "cluster-ref", os.Getenv("CLUSTER_REF"), "Only expose objects belonging to this clusterRef.")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := ctrl.GetConfigOrDie()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		logger.Error("unable to create Kubernetes client", "error", err)
		os.Exit(1)
	}
	handler, err := webui.NewServer(client, clusterRef, version, logger)
	if err != nil {
		logger.Error("unable to initialize web UI", "error", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logger.Info("starting backup and restore web UI", "address", address, "clusterRef", clusterRef, "version", version)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("web UI server exited", "error", err)
		os.Exit(1)
	}
}
