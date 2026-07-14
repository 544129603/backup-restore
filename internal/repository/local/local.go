// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/repository"
	"github.com/google/uuid"
)

type Adapter struct {
	root     string
	fileMode fs.FileMode
	dirMode  fs.FileMode
}

func New(root string) (*Adapter, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, opererrors.New(opererrors.CodeRepoPath, "local repository root must be absolute", false, nil)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, opererrors.New(opererrors.CodeRepoPath, "resolve local repository root", false, err)
	}
	if filepath.Clean(abs) == filepath.VolumeName(abs)+string(filepath.Separator) {
		return nil, opererrors.New(opererrors.CodeRepoPath, "filesystem root cannot be used as a repository", false, nil)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, opererrors.New(opererrors.CodeRepoPath, "create local repository root", false, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, opererrors.New(opererrors.CodeRepoPath, "resolve local repository symlink", false, err)
	}
	return &Adapter{root: resolved, fileMode: 0o600, dirMode: 0o700}, nil
}

func (a *Adapter) securePath(objectPath string) (string, error) {
	if objectPath == "" || filepath.IsAbs(objectPath) {
		return "", opererrors.New(opererrors.CodeRepoPath, "repository object path must be relative", false, nil)
	}
	native := filepath.FromSlash(objectPath)
	clean := filepath.Clean(native)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", opererrors.New(opererrors.CodeRepoPath, "repository path escapes root", false, nil)
	}
	joined := filepath.Join(a.root, clean)
	rel, err := filepath.Rel(a.root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", opererrors.New(opererrors.CodeRepoPath, "repository path escapes root", false, err)
	}
	current := a.root
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				break
			}
			return "", opererrors.New(opererrors.CodeRepoPath, "inspect repository path", false, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", opererrors.New(opererrors.CodeRepoPath, "symlink traversal is not allowed", false, nil)
		}
	}
	return joined, nil
}

func (a *Adapter) Check(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	name := ".health/" + uuid.NewString()
	payload := strings.NewReader("backup-restore-health-check")
	if err := a.Put(ctx, name, payload); err != nil {
		return err
	}
	renamed := name + ".renamed"
	defer func() {
		_ = a.Delete(context.WithoutCancel(ctx), name)
		_ = a.Delete(context.WithoutCancel(ctx), renamed)
	}()
	if err := a.Rename(ctx, name, renamed); err != nil {
		return err
	}
	r, err := a.Get(ctx, renamed)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(io.Discard, r)
	closeErr := r.Close()
	if copyErr != nil {
		return opererrors.New(opererrors.CodeRepoPath, "read local repository test object", true, copyErr)
	}
	if closeErr != nil {
		return opererrors.New(opererrors.CodeRepoPath, "close local repository test object", true, closeErr)
	}
	return a.Delete(ctx, renamed)
}

func (a *Adapter) Put(ctx context.Context, objectPath string, reader io.Reader) error {
	target, err := a.securePath(objectPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), a.dirMode); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "create repository object directory", false, err)
	}
	temp := target + ".tmp-" + uuid.NewString()
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, a.fileMode)
	if err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "create temporary repository object", false, err)
	}
	succeeded := false
	defer func() {
		_ = f.Close()
		if !succeeded {
			_ = os.Remove(temp)
		}
	}()
	if _, err = copyContext(ctx, f, reader); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "write repository object", true, err)
	}
	if err = f.Sync(); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "sync repository object", true, err)
	}
	if err = f.Close(); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "close repository object", true, err)
	}
	if err = os.Rename(temp, target); err != nil {
		if removeErr := os.Remove(target); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return opererrors.New(opererrors.CodeRepoPath, "replace repository object", true, err)
		}
		if err = os.Rename(temp, target); err != nil {
			return opererrors.New(opererrors.CodeRepoPath, "commit repository object", true, err)
		}
	}
	succeeded = true
	return nil
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var written int64
	for {
		select {
		case <-ctx.Done():
			return written, ctx.Err()
		default:
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			m, writeErr := dst.Write(buffer[:n])
			written += int64(m)
			if writeErr != nil {
				return written, writeErr
			}
			if m != n {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil
			}
			return written, readErr
		}
	}
}

func (a *Adapter) Get(_ context.Context, objectPath string) (io.ReadCloser, error) {
	target, err := a.securePath(objectPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(target)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (a *Adapter) Stat(_ context.Context, objectPath string) (*repository.ObjectInfo, error) {
	target, err := a.securePath(objectPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	return &repository.ObjectInfo{Path: filepath.ToSlash(objectPath), Size: info.Size(), ModTime: info.ModTime().UTC(), IsDir: info.IsDir()}, nil
}

func (a *Adapter) List(ctx context.Context, prefix string) ([]repository.ObjectInfo, error) {
	root, err := a.securePath(prefix)
	if err != nil {
		return nil, err
	}
	objects := make([]repository.ObjectInfo, 0)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if current == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fs.SkipDir
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		rel, relErr := filepath.Rel(a.root, current)
		if relErr != nil {
			return relErr
		}
		objects = append(objects, repository.ObjectInfo{Path: filepath.ToSlash(rel), Size: info.Size(), ModTime: info.ModTime().UTC(), IsDir: entry.IsDir()})
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return objects, nil
}

func (a *Adapter) Delete(_ context.Context, objectPath string) error {
	target, err := a.securePath(objectPath)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(target); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "delete repository object", true, err)
	}
	return nil
}

func (a *Adapter) Rename(_ context.Context, oldPath, newPath string) error {
	oldTarget, err := a.securePath(oldPath)
	if err != nil {
		return err
	}
	newTarget, err := a.securePath(newPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(newTarget), a.dirMode); err != nil {
		return err
	}
	if err := os.Rename(oldTarget, newTarget); err != nil {
		return opererrors.New(opererrors.CodeRepoPath, "rename repository object", true, err)
	}
	return nil
}

func (a *Adapter) MkdirAll(_ context.Context, objectPath string) error {
	target, err := a.securePath(objectPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(target, a.dirMode)
}

func (a *Adapter) AvailableBytes(_ context.Context) (int64, bool, error) {
	return availableBytes(a.root)
}
func (a *Adapter) SupportsAtomicRename() bool { return true }
func (a *Adapter) Close() error               { return nil }

func (a *Adapter) Root() string { return a.root }

func (a *Adapter) String() string { return fmt.Sprintf("local repository at %s", a.root) }

var _ repository.Repository = (*Adapter)(nil)

// Keep time imported for API stability in downstream wrappers.
