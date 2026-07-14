package local

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdapterRoundTripAndRename(t *testing.T) {
	adapter, err := New(t.TempDir())
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, adapter.Put(ctx, "record/resources.tar.gz", strings.NewReader("payload")))
	require.NoError(t, adapter.Rename(ctx, "record/resources.tar.gz", "record/resources.done"))
	r, err := adapter.Get(ctx, "record/resources.done")
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, "payload", string(data))
	require.NoError(t, adapter.Check(ctx))
}

func TestAdapterRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	adapter, err := New(root)
	require.NoError(t, err)
	require.Error(t, adapter.Put(context.Background(), "../../outside", strings.NewReader("x")))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	require.Error(t, adapter.Put(context.Background(), "link/file", strings.NewReader("x")))
}
