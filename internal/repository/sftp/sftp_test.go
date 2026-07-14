package sftp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	pkgsftp "github.com/pkg/sftp"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestConfigRequiresKnownHosts(t *testing.T) {
	_, err := New(Config{Host: "127.0.0.1", Port: 22, BasePath: "/backup", Username: "user", Password: []byte("not-a-real-secret"), ConnectTimeout: time.Second})
	require.Error(t, err)
}

func TestConfigRejectsRelativeBasePath(t *testing.T) {
	_, err := New(Config{Host: "127.0.0.1", Port: 22, BasePath: "relative", Username: "user", Password: []byte("x"), InsecureSkipHostKeyCheck: true})
	require.Error(t, err)
}

func TestAdapterAgainstInProcessSFTPServer(t *testing.T) {
	host, port, hostLine := startTestServer(t, t.TempDir())
	adapter, err := New(Config{Host: host, Port: int32(port), BasePath: "/base", Username: "test", Password: []byte("secret"), KnownHosts: []byte(hostLine + "\n"), ConnectTimeout: 2 * time.Second, OperationTimeout: 5 * time.Second, MaxConnections: 2})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adapter.Close()) })
	ctx := context.Background()
	require.NoError(t, adapter.Check(ctx))
	require.NoError(t, adapter.Put(ctx, "records/a.txt", bytes.NewBufferString("hello")))
	reader, err := adapter.Get(ctx, "records/a.txt")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "hello", string(data))
	require.NoError(t, adapter.Rename(ctx, "records/a.txt", "records/b.txt"))
	info, err := adapter.Stat(ctx, "records/b.txt")
	require.NoError(t, err)
	require.EqualValues(t, 5, info.Size)
	require.NoError(t, adapter.Delete(ctx, "records/b.txt"))
}

func startTestServer(t *testing.T, root string) (string, int, string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "base"), 0o700))
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(private)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if metadata.User() == "test" && string(password) == "secret" {
			return nil, nil
		}
		return nil, fmt.Errorf("authentication rejected")
	}}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, portText, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	var wait sync.WaitGroup
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			wait.Add(1)
			go func() {
				defer wait.Done()
				serverConnection, channels, requests, handshakeErr := ssh.NewServerConn(connection, serverConfig)
				if handshakeErr != nil {
					_ = connection.Close()
					return
				}
				defer serverConnection.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					channel, channelRequests, channelErr := newChannel.Accept()
					if channelErr != nil {
						continue
					}
					go func() {
						for request := range channelRequests {
							ok := request.Type == "subsystem" && len(request.Payload) >= 4 && string(request.Payload[4:]) == "sftp"
							_ = request.Reply(ok, nil)
						}
					}()
					server, serverErr := pkgsftp.NewServer(channel, pkgsftp.WithServerWorkingDirectory(root))
					if serverErr == nil {
						serveErr := server.Serve()
						if serveErr != nil && serveErr != io.EOF {
							_ = server.Close()
						}
						_ = server.Close()
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		wait.Wait()
	})
	hostPattern := net.JoinHostPort(host, strconv.Itoa(port))
	return host, port, knownhosts.Line([]string{hostPattern}, signer.PublicKey())
}
