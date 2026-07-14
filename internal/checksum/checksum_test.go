package checksum

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSumAndManifest(t *testing.T) {
	sum, size, err := Sum(context.Background(), strings.NewReader("hello"))
	require.NoError(t, err)
	require.EqualValues(t, 5, size)
	ok, _, err := Verify(context.Background(), strings.NewReader("hello"), sum)
	require.NoError(t, err)
	require.True(t, ok)
	manifest := Manifest(map[string]string{"b": sum, "a": sum})
	require.True(t, strings.Index(manifest, "  a") < strings.Index(manifest, "  b"))
	parsed, err := ParseManifest(strings.NewReader(manifest))
	require.NoError(t, err)
	require.Equal(t, sum, parsed["a"])
}
