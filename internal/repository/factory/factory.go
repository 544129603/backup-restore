// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package factory

import (
	"context"
	"fmt"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	protectionv1alpha1 "github.com/example/backup-restore-operator/api/v1alpha1"
	"github.com/example/backup-restore-operator/internal/repository"
	"github.com/example/backup-restore-operator/internal/repository/local"
	sftprepo "github.com/example/backup-restore-operator/internal/repository/sftp"
)

type Factory struct{ Client client.Reader }

func (f Factory) New(ctx context.Context, spec *protectionv1alpha1.BackupRepository) (repository.Repository, error) {
	switch spec.Spec.Type {
	case protectionv1alpha1.RepositoryTypeLocal:
		root := spec.Spec.Local.Path
		if spec.Spec.Local.Mode == protectionv1alpha1.LocalModePVC {
			root = filepath.Join(spec.Spec.Local.PVC.MountPath, filepath.FromSlash(spec.Spec.Local.PVC.SubPath))
		}
		return local.New(root)
	case protectionv1alpha1.RepositoryTypeSFTP:
		s := spec.Spec.SFTP
		username, err := f.SecretValue(ctx, &s.Auth.UsernameRef)
		if err != nil {
			return nil, err
		}
		config := sftprepo.Config{Host: s.Host, Port: s.Port, BasePath: s.BasePath, Username: string(username), InsecureSkipHostKeyCheck: s.InsecureSkipHostKeyCheck, ConnectTimeout: s.ConnectTimeout.Duration, OperationTimeout: s.OperationTimeout.Duration, KeepAliveInterval: s.KeepAliveInterval.Duration, MaxConnections: int(s.MaxConnections)}
		if s.Auth.PasswordRef != nil {
			config.Password, err = f.SecretValue(ctx, s.Auth.PasswordRef)
		}
		if s.Auth.PrivateKeyRef != nil {
			config.PrivateKey, err = f.SecretValue(ctx, s.Auth.PrivateKeyRef)
		}
		if err != nil {
			return nil, err
		}
		if s.Auth.PassphraseRef != nil {
			config.Passphrase, err = f.SecretValue(ctx, s.Auth.PassphraseRef)
			if err != nil {
				return nil, err
			}
		}
		if s.KnownHostsRef != nil {
			config.KnownHosts, err = f.SecretValue(ctx, s.KnownHostsRef)
			if err != nil {
				return nil, err
			}
		}
		return sftprepo.New(config)
	default:
		return nil, fmt.Errorf("unsupported repository type %q", spec.Spec.Type)
	}
}

func (f Factory) SecretValue(ctx context.Context, ref *protectionv1alpha1.SecretKeyReference) ([]byte, error) {
	if ref == nil {
		return nil, fmt.Errorf("secret reference is required")
	}
	secret := &corev1.Secret{}
	if err := f.Client.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("read referenced secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("referenced secret %s/%s does not contain key %s", ref.Namespace, ref.Name, ref.Key)
	}
	return append([]byte(nil), value...), nil
}
