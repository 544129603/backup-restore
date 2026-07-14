// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package archive

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func Create(ctx context.Context, root string, output io.Writer, level int) error {
	if level == 0 {
		level = gzip.DefaultCompression
	}
	gzipWriter, err := gzip.NewWriterLevel(output, level)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	closeWith := func(primary error) error {
		tarErr := tarWriter.Close()
		gzipErr := gzipWriter.Close()
		if primary != nil {
			return primary
		}
		if tarErr != nil {
			return tarErr
		}
		return gzipErr
	}
	paths := make([]string, 0)
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s is not allowed in archive", current)
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return closeWith(err)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		select {
		case <-ctx.Done():
			return closeWith(ctx.Err())
		default:
		}
		full := filepath.Join(root, rel)
		info, err := os.Stat(full)
		if err != nil {
			return closeWith(err)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return closeWith(err)
		}
		header.Name = filepath.ToSlash(rel)
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid = 0
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		if info.IsDir() {
			header.Mode = 0o700
		} else {
			header.Mode = 0o600
		}
		if err = tarWriter.WriteHeader(header); err != nil {
			return closeWith(err)
		}
		if info.IsDir() {
			continue
		}
		file, err := os.Open(full)
		if err != nil {
			return closeWith(err)
		}
		_, copyErr := copyContext(ctx, tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			return closeWith(copyErr)
		}
		if closeErr != nil {
			return closeWith(closeErr)
		}
	}
	return closeWith(nil)
}

func Extract(ctx context.Context, input io.Reader, destination string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = 20 << 30
	}
	gzipReader, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var extracted int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Name == "" || filepath.IsAbs(header.Name) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes destination: %q", header.Name)
		}
		target := filepath.Join(destination, clean)
		rel, err := filepath.Rel(destination, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes destination: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			extracted += header.Size
			if extracted > maxBytes {
				return fmt.Errorf("archive exceeds extraction limit")
			}
			if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return err
			}
			_, copyErr := copyContext(ctx, file, tarReader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported archive entry type %d", header.Typeflag)
		}
	}
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 64*1024)
	var total int64
	for {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, err := src.Read(buffer)
		if n > 0 {
			m, werr := dst.Write(buffer[:n])
			total += int64(m)
			if werr != nil {
				return total, werr
			}
			if m != n {
				return total, io.ErrShortWrite
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return total, err
		}
	}
}
