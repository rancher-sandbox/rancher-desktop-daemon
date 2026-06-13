// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestLoadManifestParsesOctalAndDefaults(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "overlay.yaml")
	assert.NilError(t, os.WriteFile(manifest, []byte(`
entries:
  - path: /usr/local/bin/tool
    source: tool
    mode: "0755"
  - path: /etc/keep
    type: dir
`), 0o644))

	m, err := LoadManifest(manifest)
	assert.NilError(t, err)
	assert.Equal(t, len(m.Entries), 2)

	mode, err := m.Entries[0].mode(0o644)
	assert.NilError(t, err)
	assert.Equal(t, mode, os.FileMode(0o755))
	assert.Equal(t, m.Entries[1].kind(), TypeDir)
}

func TestValidateRejectsBadEntries(t *testing.T) {
	cases := map[string]Entry{
		"relative path":   {Path: "usr/local/bin/x", Source: "x"},
		"file no source":  {Path: "/x"},
		"symlink target":  {Path: "/x", Type: TypeSymlink},
		"escaping source": {Path: "/x", Source: "../x"},
		"bad mode":        {Path: "/x", Source: "x", Mode: "9999"},
		"bad mtime":       {Path: "/x", Source: "x", Mtime: "last tuesday"},
		"unknown type":    {Path: "/x", Type: "socket"},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Assert(t, e.validate() != nil, "expected validation error")
		})
	}
}

func TestApplyTarOverridesNewFilesDirsAndLinks(t *testing.T) {
	base := buildTar(t, []tarItem{
		{name: "usr/local/bin/", dir: true},
		{name: "usr/local/bin/old", body: "OLD"},
		{name: "etc/keep.conf", body: "KEEP"},
	})

	source := t.TempDir()
	writeSource(t, source, "bin/old", "NEWOLD")
	writeSource(t, source, "bin/new", "NEW")

	m := &Manifest{Entries: []Entry{
		{Path: "/usr/local/bin/old", Source: "bin/old", Mode: "0755", UID: 1, GID: 2},
		{Path: "/usr/local/bin/new", Source: "bin/new", Mode: "0700"},
		{Path: "/etc/foo.d", Type: TypeDir, Mode: "0750", UID: 3, GID: 4},
		{Path: "/etc/link", Type: TypeSymlink, Target: "/usr/local/bin/new"},
	}}

	var out bytes.Buffer
	assert.NilError(t, ApplyTar(bytes.NewReader(base), &out, m, source))
	got := readTar(t, out.Bytes())

	override := got["usr/local/bin/old"]
	assert.Equal(t, override.body, "NEWOLD")
	assert.Equal(t, override.hdr.Mode, int64(0o755))
	assert.Equal(t, override.hdr.Uid, 1)
	assert.Equal(t, override.hdr.Gid, 2)

	added := got["usr/local/bin/new"]
	assert.Equal(t, added.body, "NEW")
	assert.Equal(t, added.hdr.Mode, int64(0o700))

	assert.Equal(t, got["etc/keep.conf"].body, "KEEP")

	dir := got["etc/foo.d"]
	assert.Equal(t, dir.hdr.Typeflag, byte(tar.TypeDir))
	assert.Equal(t, dir.hdr.Mode, int64(0o750))
	assert.Equal(t, dir.hdr.Uid, 3)

	link := got["etc/link"]
	assert.Equal(t, link.hdr.Typeflag, byte(tar.TypeSymlink))
	assert.Equal(t, link.hdr.Linkname, "/usr/local/bin/new")

	assert.Equal(t, count(out.Bytes(), "usr/local/bin/old"), 1)
}

func TestApplyTarTimestamps(t *testing.T) {
	source := t.TempDir()
	writeSource(t, source, "keep", "K")
	writeSource(t, source, "over", "O")
	srcTime := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	assert.NilError(t, os.Chtimes(filepath.Join(source, "keep"), srcTime, srcTime))

	m := &Manifest{Entries: []Entry{
		{Path: "/keep", Source: "keep"},                                // keeps source mtime
		{Path: "/over", Source: "over", Mtime: "2020-01-02T03:04:05Z"}, // override
	}}

	var out bytes.Buffer
	assert.NilError(t, ApplyTar(bytes.NewReader(buildTar(t, nil)), &out, m, source))
	got := readTar(t, out.Bytes())

	assert.Equal(t, got["keep"].hdr.ModTime.Unix(), srcTime.Unix())
	assert.Equal(t, got["over"].hdr.ModTime.Unix(), time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).Unix())
}

type tarItem struct {
	name string
	body string
	dir  bool
}

func buildTar(t *testing.T, items []tarItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, it := range items {
		hdr := &tar.Header{Name: it.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(it.body))}
		if it.dir {
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		}
		assert.NilError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(it.body))
		assert.NilError(t, err)
	}
	assert.NilError(t, tw.Close())
	return buf.Bytes()
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func readTar(t *testing.T, data []byte) map[string]tarEntry {
	t.Helper()
	out := map[string]tarEntry{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		assert.NilError(t, err)
		body, err := io.ReadAll(tr)
		assert.NilError(t, err)
		name := hdr.Name
		if hdr.Typeflag == tar.TypeDir {
			name = name[:len(name)-1] // drop trailing slash for lookup
		}
		out[name] = tarEntry{hdr: *hdr, body: string(body)}
	}
	return out
}

func writeSource(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	assert.NilError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	assert.NilError(t, os.WriteFile(full, []byte(body), 0o644))
}

func count(data []byte, name string) int {
	n := 0
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		if hdr.Name == name {
			n++
		}
	}
	return n
}
