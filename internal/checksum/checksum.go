// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package checksum

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

func Sum(ctx context.Context, reader io.Reader) (string, int64, error) {
	hash := sha256.New()
	buffer := make([]byte, 64*1024)
	var size int64
	for {
		select {
		case <-ctx.Done():
			return "", size, ctx.Err()
		default:
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			written, writeErr := hash.Write(buffer[:n])
			size += int64(written)
			if writeErr != nil {
				return "", size, writeErr
			}
		}
		if err != nil {
			if err == io.EOF {
				return hex.EncodeToString(hash.Sum(nil)), size, nil
			}
			return "", size, err
		}
	}
}

func Verify(ctx context.Context, reader io.Reader, expected string) (bool, int64, error) {
	actual, size, err := Sum(ctx, reader)
	if err != nil {
		return false, size, err
	}
	return strings.EqualFold(actual, expected), size, nil
}

func Manifest(entries map[string]string) string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sortStrings(keys)
	var builder strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&builder, "%s  %s\n", entries[key], key)
	}
	return builder.String()
}

func ParseManifest(reader io.Reader) (map[string]string, error) {
	entries := map[string]string{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid checksum manifest line")
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return nil, fmt.Errorf("invalid checksum: %w", err)
		}
		entries[parts[1]] = strings.ToLower(parts[0])
	}
	return entries, scanner.Err()
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
