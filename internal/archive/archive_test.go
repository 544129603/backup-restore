package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeterministicArchiveAndExtract(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "resources"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "resources", "a.json"), []byte("{}"), 0o600))
	var first, second bytes.Buffer
	require.NoError(t, Create(context.Background(), root, &first, 6))
	require.NoError(t, Create(context.Background(), root, &second, 6))
	require.Equal(t, first.Bytes(), second.Bytes())
	destination := t.TempDir()
	require.NoError(t, Extract(context.Background(), bytes.NewReader(first.Bytes()), destination, 1024))
	data, err := os.ReadFile(filepath.Join(destination, "resources", "a.json"))
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))
}

func TestExtractRejectsTraversal(t *testing.T) {
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tw := tar.NewWriter(gz)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}))
	_, err := tw.Write([]byte("x"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.Error(t, Extract(context.Background(), bytes.NewReader(buffer.Bytes()), t.TempDir(), 100))
}
