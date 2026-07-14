package encryption

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAESGCMRoundTripAndTamperDetection(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plain := bytes.Repeat([]byte("backup-data"), 200000)
	var encrypted bytes.Buffer
	require.NoError(t, Encrypt(context.Background(), key, &encrypted, bytes.NewReader(plain)))
	var restored bytes.Buffer
	require.NoError(t, Decrypt(context.Background(), key, &restored, bytes.NewReader(encrypted.Bytes())))
	require.Equal(t, plain, restored.Bytes())
	tampered := append([]byte(nil), encrypted.Bytes()...)
	tampered[len(tampered)-1] ^= 1
	require.Error(t, Decrypt(context.Background(), key, &bytes.Buffer{}, bytes.NewReader(tampered)))
}

func TestRejectInvalidKey(t *testing.T) {
	require.Error(t, Encrypt(context.Background(), []byte("short"), &bytes.Buffer{}, bytes.NewReader(nil)))
}
