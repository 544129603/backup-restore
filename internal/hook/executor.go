// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

// Package hook defines the application-consistency extension point. V1.0
// intentionally ships a disabled implementation; it never executes arbitrary
// commands from a CR.
package hook

import (
	"context"
	"fmt"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
)

type Result struct {
	Name     string
	ExitCode int32
	Output   string
}

type Executor interface {
	RunPre(context.Context, protectionv1alpha1.HookSpec) ([]Result, error)
	RunPost(context.Context, protectionv1alpha1.HookSpec) ([]Result, error)
}

type Disabled struct{}

func (Disabled) RunPre(_ context.Context, spec protectionv1alpha1.HookSpec) ([]Result, error) {
	return reject(spec)
}
func (Disabled) RunPost(_ context.Context, spec protectionv1alpha1.HookSpec) ([]Result, error) {
	return reject(spec)
}
func reject(spec protectionv1alpha1.HookSpec) ([]Result, error) {
	if len(spec.Pre) == 0 && len(spec.Post) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("application-consistency hooks are disabled in v1.0")
}
