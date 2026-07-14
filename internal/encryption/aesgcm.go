// Copyright 2026.
// SPDX-License-Identifier: Apache-2.0

package encryption

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

var magic = [8]byte{'B', 'R', 'G', 'C', 'M', '0', '0', '1'}

const chunkSize = 1 << 20

// Encrypt writes a versioned chunked AES-256-GCM stream. Each chunk uses a
// unique nonce composed of one random prefix and a monotonic counter.
func Encrypt(ctx context.Context, key []byte, dst io.Writer, src io.Reader) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	prefix := make([]byte, 4)
	if _, err = io.ReadFull(rand.Reader, prefix); err != nil {
		return err
	}
	if _, err = dst.Write(magic[:]); err != nil {
		return err
	}
	if _, err = dst.Write(prefix); err != nil {
		return err
	}
	buffer := make([]byte, chunkSize)
	var counter uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := io.ReadFull(src, buffer)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return readErr
		}
		if n > 0 {
			nonce := makeNonce(prefix, counter)
			sealed := aead.Seal(nil, nonce, buffer[:n], magic[:])
			if len(sealed) > int(^uint32(0)) {
				return fmt.Errorf("encrypted chunk is too large")
			}
			var length [4]byte
			binary.BigEndian.PutUint32(length[:], uint32(len(sealed)))
			if _, err = dst.Write(length[:]); err != nil {
				return err
			}
			if _, err = dst.Write(sealed); err != nil {
				return err
			}
			counter++
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			return nil
		}
	}
}

func Decrypt(ctx context.Context, key []byte, dst io.Writer, src io.Reader) error {
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	header := make([]byte, len(magic))
	if _, err = io.ReadFull(src, header); err != nil {
		return fmt.Errorf("read encryption header: %w", err)
	}
	if string(header) != string(magic[:]) {
		return fmt.Errorf("unsupported encrypted stream format")
	}
	prefix := make([]byte, 4)
	if _, err = io.ReadFull(src, prefix); err != nil {
		return fmt.Errorf("read nonce prefix: %w", err)
	}
	var counter uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		var length [4]byte
		_, readErr := io.ReadFull(src, length[:])
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read encrypted chunk length: %w", readErr)
		}
		size := binary.BigEndian.Uint32(length[:])
		if size < uint32(aead.Overhead()) || size > chunkSize+uint32(aead.Overhead()) {
			return fmt.Errorf("invalid encrypted chunk size %d", size)
		}
		sealed := make([]byte, int(size))
		if _, err = io.ReadFull(src, sealed); err != nil {
			return fmt.Errorf("read encrypted chunk: %w", err)
		}
		plain, openErr := aead.Open(nil, makeNonce(prefix, counter), sealed, magic[:])
		if openErr != nil {
			return fmt.Errorf("authenticate encrypted chunk: %w", openErr)
		}
		if _, err = dst.Write(plain); err != nil {
			return err
		}
		counter++
	}
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("AES-256-GCM key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func makeNonce(prefix []byte, counter uint64) []byte {
	nonce := make([]byte, 12)
	copy(nonce, prefix)
	binary.BigEndian.PutUint64(nonce[4:], counter)
	return nonce
}
