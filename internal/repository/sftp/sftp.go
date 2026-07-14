// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package sftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	opererrors "github.com/example/backup-restore-operator/internal/errors"
	"github.com/example/backup-restore-operator/internal/repository"
	"github.com/google/uuid"
	pkgsftp "github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Config struct {
	Host                     string
	Port                     int32
	BasePath                 string
	Username                 string
	Password                 []byte
	PrivateKey               []byte
	Passphrase               []byte
	KnownHosts               []byte
	InsecureSkipHostKeyCheck bool
	ConnectTimeout           time.Duration
	OperationTimeout         time.Duration
	KeepAliveInterval        time.Duration
	MaxConnections           int
}

type Adapter struct {
	config    Config
	sshConfig *ssh.ClientConfig
	semaphore chan struct{}
	closeOnce sync.Once
	closed    chan struct{}
}

func New(config Config) (*Adapter, error) {
	if config.Host == "" || config.Port < 1 || config.Port > 65535 || !path.IsAbs(config.BasePath) || path.Clean(config.BasePath) != config.BasePath {
		return nil, opererrors.New(opererrors.CodeRepoPath, "invalid SFTP host, port or base path", false, nil)
	}
	if config.Username == "" {
		return nil, opererrors.New(opererrors.CodeRepoAuth, "SFTP username is required", false, nil)
	}
	auth := make([]ssh.AuthMethod, 0, 1)
	if len(config.PrivateKey) > 0 {
		var signer ssh.Signer
		var err error
		if len(config.Passphrase) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(config.PrivateKey, config.Passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(config.PrivateKey)
		}
		if err != nil {
			return nil, opererrors.New(opererrors.CodeRepoAuth, "parse SFTP private key", false, err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	} else if len(config.Password) > 0 {
		auth = append(auth, ssh.Password(string(config.Password)))
	} else {
		return nil, opererrors.New(opererrors.CodeRepoAuth, "SFTP password or private key is required", false, nil)
	}
	var hostKey ssh.HostKeyCallback
	if config.InsecureSkipHostKeyCheck {
		hostKey = ssh.InsecureIgnoreHostKey()
	} else {
		if len(config.KnownHosts) == 0 {
			return nil, opererrors.New(opererrors.CodeRepoAuth, "known_hosts is required", false, nil)
		}
		file, err := os.CreateTemp("", "backup-restore-known-hosts-*")
		if err != nil {
			return nil, err
		}
		name := file.Name()
		defer os.Remove(name)
		if err = file.Chmod(0o600); err == nil {
			_, err = file.Write(config.KnownHosts)
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, opererrors.New(opererrors.CodeRepoAuth, "prepare known_hosts", false, err)
		}
		hostKey, err = knownhosts.New(name)
		if err != nil {
			return nil, opererrors.New(opererrors.CodeRepoAuth, "parse known_hosts", false, err)
		}
	}
	if config.ConnectTimeout <= 0 {
		config.ConnectTimeout = 10 * time.Second
	}
	if config.OperationTimeout <= 0 {
		config.OperationTimeout = 5 * time.Minute
	}
	if config.KeepAliveInterval <= 0 {
		config.KeepAliveInterval = 30 * time.Second
	}
	if config.MaxConnections <= 0 {
		config.MaxConnections = 4
	}
	return &Adapter{config: config, sshConfig: &ssh.ClientConfig{User: config.Username, Auth: auth, HostKeyCallback: hostKey, Timeout: config.ConnectTimeout}, semaphore: make(chan struct{}, config.MaxConnections), closed: make(chan struct{})}, nil
}

func (a *Adapter) acquire(ctx context.Context) error {
	select {
	case a.semaphore <- struct{}{}:
		return nil
	case <-a.closed:
		return fmt.Errorf("repository is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (a *Adapter) release() { <-a.semaphore }

func (a *Adapter) connect(ctx context.Context) (*ssh.Client, *pkgsftp.Client, error) {
	if err := a.acquire(ctx); err != nil {
		return nil, nil, err
	}
	succeeded := false
	defer func() {
		if !succeeded {
			a.release()
		}
	}()
	dialer := net.Dialer{Timeout: a.config.ConnectTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.config.Host, fmt.Sprintf("%d", a.config.Port)))
	if err != nil {
		return nil, nil, opererrors.New(opererrors.CodeRepoConnect, "connect to SFTP server", true, err)
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, net.JoinHostPort(a.config.Host, fmt.Sprintf("%d", a.config.Port)), a.sshConfig)
	if err != nil {
		_ = connection.Close()
		code := opererrors.CodeRepoConnect
		if strings.Contains(strings.ToLower(err.Error()), "unable to authenticate") {
			code = opererrors.CodeRepoAuth
		}
		return nil, nil, opererrors.New(code, "establish SSH connection", code == opererrors.CodeRepoConnect, err)
	}
	sshClient := ssh.NewClient(clientConnection, channels, requests)
	sftpClient, err := pkgsftp.NewClient(sshClient)
	if err != nil {
		_ = sshClient.Close()
		return nil, nil, opererrors.New(opererrors.CodeRepoConnect, "open SFTP subsystem", true, err)
	}
	succeeded = true
	return sshClient, sftpClient, nil
}

func (a *Adapter) withClient(ctx context.Context, fn func(*pkgsftp.Client) error) error {
	operationCtx, cancel := context.WithTimeout(ctx, a.config.OperationTimeout)
	defer cancel()
	sshClient, client, err := a.connect(operationCtx)
	if err != nil {
		return err
	}
	defer a.release()
	defer sshClient.Close()
	defer client.Close()
	stopKeepAlive := startKeepAlive(sshClient, a.config.KeepAliveInterval)
	defer close(stopKeepAlive)
	done := make(chan error, 1)
	go func() { done <- fn(client) }()
	select {
	case err = <-done:
		return err
	case <-operationCtx.Done():
		return opererrors.New(opererrors.CodeRepoConnect, "SFTP operation timed out", true, operationCtx.Err())
	}
}

func (a *Adapter) remotePath(objectPath string) (string, error) {
	if objectPath == "" || path.IsAbs(objectPath) || path.Clean(objectPath) != objectPath || objectPath == ".." || strings.HasPrefix(objectPath, "../") {
		return "", opererrors.New(opererrors.CodeRepoPath, "SFTP object path must be safe and relative", false, nil)
	}
	joined := path.Join(a.config.BasePath, objectPath)
	if joined != a.config.BasePath && !strings.HasPrefix(joined, strings.TrimSuffix(a.config.BasePath, "/")+"/") {
		return "", opererrors.New(opererrors.CodeRepoPath, "SFTP path escapes base path", false, nil)
	}
	return joined, nil
}

func (a *Adapter) Check(ctx context.Context) error {
	name := ".health/" + uuid.NewString()
	renamed := name + ".renamed"
	if err := a.Put(ctx, name, strings.NewReader("backup-restore-health-check")); err != nil {
		return err
	}
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
	_, err = io.Copy(io.Discard, r)
	closeErr := r.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return a.Delete(ctx, renamed)
}

func (a *Adapter) Put(ctx context.Context, objectPath string, reader io.Reader) error {
	target, err := a.remotePath(objectPath)
	if err != nil {
		return err
	}
	return a.withClient(ctx, func(client *pkgsftp.Client) error {
		if err := client.MkdirAll(path.Dir(target)); err != nil {
			return opererrors.New(opererrors.CodeRepoPath, "create SFTP directory", false, err)
		}
		temp := target + ".part-" + uuid.NewString()
		file, err := client.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return opererrors.New(opererrors.CodeRepoPath, "create SFTP temporary file", false, err)
		}
		succeeded := false
		defer func() {
			_ = file.Close()
			if !succeeded {
				_ = client.Remove(temp)
			}
		}()
		if _, err = copyContext(ctx, file, reader); err != nil {
			return opererrors.New(opererrors.CodeRepoConnect, "upload SFTP object", true, err)
		}
		if err = file.Close(); err != nil {
			return opererrors.New(opererrors.CodeRepoConnect, "close SFTP object", true, err)
		}
		if err = client.Rename(temp, target); err != nil {
			_ = client.Remove(target)
			if err = client.Rename(temp, target); err != nil {
				return opererrors.New(opererrors.CodeRepoPath, "commit SFTP object", true, err)
			}
		}
		succeeded = true
		return nil
	})
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

type remoteReadCloser struct {
	file          *pkgsftp.File
	client        *pkgsftp.Client
	sshClient     *ssh.Client
	release       func()
	closeOnce     sync.Once
	closeError    error
	stopKeepAlive chan struct{}
}

func (r *remoteReadCloser) Read(p []byte) (int, error) { return r.file.Read(p) }

func (r *remoteReadCloser) Close() error {
	r.closeOnce.Do(func() {
		close(r.stopKeepAlive)
		fileErr := r.file.Close()
		clientErr := r.client.Close()
		sshErr := r.sshClient.Close()
		r.release()
		if fileErr != nil {
			r.closeError = fileErr
		} else if clientErr != nil {
			r.closeError = clientErr
		} else {
			r.closeError = sshErr
		}
	})
	return r.closeError
}

func (a *Adapter) Get(ctx context.Context, objectPath string) (io.ReadCloser, error) {
	target, err := a.remotePath(objectPath)
	if err != nil {
		return nil, err
	}
	sshClient, client, err := a.connect(ctx)
	if err != nil {
		return nil, err
	}
	file, err := client.Open(target)
	if err != nil {
		_ = client.Close()
		_ = sshClient.Close()
		a.release()
		return nil, err
	}
	return &remoteReadCloser{file: file, client: client, sshClient: sshClient, release: a.release, stopKeepAlive: startKeepAlive(sshClient, a.config.KeepAliveInterval)}, nil
}

func startKeepAlive(client *ssh.Client, interval time.Duration) chan struct{} {
	stop := make(chan struct{})
	if interval <= 0 {
		return stop
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_, _, _ = client.SendRequest("keepalive@openssh.com", true, nil)
			case <-stop:
				return
			}
		}
	}()
	return stop
}

func (a *Adapter) Stat(ctx context.Context, objectPath string) (*repository.ObjectInfo, error) {
	target, err := a.remotePath(objectPath)
	if err != nil {
		return nil, err
	}
	var result *repository.ObjectInfo
	err = a.withClient(ctx, func(client *pkgsftp.Client) error {
		info, err := client.Stat(target)
		if err != nil {
			return err
		}
		result = &repository.ObjectInfo{Path: objectPath, Size: info.Size(), ModTime: info.ModTime().UTC(), IsDir: info.IsDir()}
		return nil
	})
	return result, err
}

func (a *Adapter) List(ctx context.Context, prefix string) ([]repository.ObjectInfo, error) {
	target, err := a.remotePath(prefix)
	if err != nil {
		return nil, err
	}
	results := make([]repository.ObjectInfo, 0)
	err = a.withClient(ctx, func(client *pkgsftp.Client) error {
		walker := client.Walk(target)
		for walker.Step() {
			if walker.Err() != nil {
				return walker.Err()
			}
			if walker.Path() == target {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(walker.Path(), a.config.BasePath), "/")
			info := walker.Stat()
			results = append(results, repository.ObjectInfo{Path: rel, Size: info.Size(), ModTime: info.ModTime().UTC(), IsDir: info.IsDir()})
		}
		return nil
	})
	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results, err
}

func (a *Adapter) Delete(ctx context.Context, objectPath string) error {
	target, err := a.remotePath(objectPath)
	if err != nil {
		return err
	}
	return a.withClient(ctx, func(client *pkgsftp.Client) error {
		info, err := client.Stat(target)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return client.RemoveDirectory(target)
		}
		return client.Remove(target)
	})
}

func (a *Adapter) Rename(ctx context.Context, oldPath, newPath string) error {
	oldTarget, err := a.remotePath(oldPath)
	if err != nil {
		return err
	}
	newTarget, err := a.remotePath(newPath)
	if err != nil {
		return err
	}
	return a.withClient(ctx, func(client *pkgsftp.Client) error {
		if err := client.MkdirAll(path.Dir(newTarget)); err != nil {
			return err
		}
		return client.Rename(oldTarget, newTarget)
	})
}

func (a *Adapter) MkdirAll(ctx context.Context, objectPath string) error {
	target, err := a.remotePath(objectPath)
	if err != nil {
		return err
	}
	return a.withClient(ctx, func(client *pkgsftp.Client) error { return client.MkdirAll(target) })
}
func (a *Adapter) AvailableBytes(ctx context.Context) (int64, bool, error) {
	var available int64
	known := false
	err := a.withClient(ctx, func(client *pkgsftp.Client) error {
		stat, err := client.StatVFS(a.config.BasePath)
		if err != nil {
			// statvfs@openssh.com is optional. Read/write Check remains the
			// authoritative health signal when the server lacks the extension.
			return nil
		}
		free := stat.FreeSpace()
		if free > uint64(^uint64(0)>>1) {
			return fmt.Errorf("remote free-space value overflows int64")
		}
		available, known = int64(free), true
		return nil
	})
	return available, known, err
}
func (a *Adapter) SupportsAtomicRename() bool { return true }
func (a *Adapter) Close() error               { a.closeOnce.Do(func() { close(a.closed) }); return nil }

var _ repository.Repository = (*Adapter)(nil)
