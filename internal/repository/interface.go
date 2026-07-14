// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package repository

import (
	"context"
	"io"
	"time"
)

type ObjectInfo struct {
	Path    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

type Repository interface {
	Check(ctx context.Context) error
	Put(ctx context.Context, objectPath string, reader io.Reader) error
	Get(ctx context.Context, objectPath string) (io.ReadCloser, error)
	Stat(ctx context.Context, objectPath string) (*ObjectInfo, error)
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)
	Delete(ctx context.Context, objectPath string) error
	Rename(ctx context.Context, oldPath, newPath string) error
	MkdirAll(ctx context.Context, objectPath string) error
	AvailableBytes(ctx context.Context) (bytes int64, known bool, err error)
	SupportsAtomicRename() bool
	Close() error
}
