// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package xz

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// samplePlaintext is the content the tests compress and decode back. It is built
// from \n literals so the expectation does not depend on how git checks out line
// endings, and it is large enough to drive the decode loop over many reads.
func samplePlaintext() []byte {
	const block = "rancher-desktop-daemon in-process xz decoder fixture.\n" +
		"The quick brown fox jumps over the lazy dog.\n"
	return bytes.Repeat([]byte(block), 100_000)
}

// xzCompress compresses data with the system xz CLI, so the tests exercise the
// decoder against real xz output rather than our own encoder. It skips when no
// xz is installed: the decoder runs without xz, but producing its test input
// needs one.
func xzCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz not found in PATH")
	}
	cmd := exec.CommandContext(t.Context(), "xz", "--compress", "--stdout")
	cmd.Stdin = bytes.NewReader(data)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	assert.NilError(t, cmd.Run(), "xz failed: %s", stderr.String())
	return out.Bytes()
}

func TestDecompress(t *testing.T) {
	want := samplePlaintext()
	compressed := xzCompress(t, want)

	var got bytes.Buffer
	assert.NilError(t, Decompress(t.Context(), bytes.NewReader(compressed), &got))
	assert.Assert(t, bytes.Equal(got.Bytes(), want))
}

func TestDecompressTruncated(t *testing.T) {
	compressed := xzCompress(t, samplePlaintext())

	var got bytes.Buffer
	err := Decompress(t.Context(), bytes.NewReader(compressed[:len(compressed)/2]), &got)
	assert.Assert(t, err != nil, "truncated xz stream must fail")
}

func TestDecompressFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.xz")
	want := samplePlaintext()
	assert.NilError(t, os.WriteFile(src, xzCompress(t, want), 0o644))

	dst := filepath.Join(dir, "image")
	assert.NilError(t, DecompressFile(t.Context(), src, dst))

	got, err := os.ReadFile(dst)
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(got, want))
	assertNoTempFiles(t, dir)
}

func TestDecompressFileLeavesNoPartialOutput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "truncated.xz")
	compressed := xzCompress(t, samplePlaintext())
	assert.NilError(t, os.WriteFile(src, compressed[:len(compressed)/2], 0o644))

	dst := filepath.Join(dir, "image")
	assert.Assert(t, DecompressFile(t.Context(), src, dst) != nil)

	_, err := os.Stat(dst)
	assert.Assert(t, os.IsNotExist(err), "dst must not exist after a failed decode")
	assertNoTempFiles(t, dir)
}

// A canceled context must abort the decode and leave neither a partial output
// nor a leftover temp file behind.
func TestDecompressFileCanceledContext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.xz")
	assert.NilError(t, os.WriteFile(src, xzCompress(t, samplePlaintext()), 0o644))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dst := filepath.Join(dir, "image")
	err := DecompressFile(ctx, src, dst)
	assert.Assert(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)

	_, statErr := os.Stat(dst)
	assert.Assert(t, os.IsNotExist(statErr), "dst must not exist after a canceled decode")
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	assert.NilError(t, err)
	assert.Assert(t, len(matches) == 0, "leftover temp files: %v", matches)
}
