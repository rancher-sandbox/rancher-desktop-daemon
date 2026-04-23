// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package socketbridge_test

import (
	"io"
	"testing"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/socketbridge"
)

// splitConn wraps separate read/write ends so the external test code and the
// Pipe function each own one side of the IO.  It is meant to satisfy HalfCloser functionality.
//
// How the test is layed out (a and b are two individual and unrelated connections):
//
//	external→aIn_w  [pipe1]  aIn_r→a.Read   Pipe copies→  b.Write→bOut_w  [pipe3]  bOut_r→external
//	external→bIn_w  [pipe2]  bIn_r→b.Read   Pipe copies→  a.Write→aOut_w  [pipe4]  aOut_r→external
type splitConn struct {
	r          *io.PipeReader
	w          *io.PipeWriter
	writeDone  bool
}

func newSplitConnPair() (a, b *splitConn, aIn, bIn *io.PipeWriter, aOut, bOut *io.PipeReader) {
	aIn_r, aIn_w := io.Pipe()
	bIn_r, bIn_w := io.Pipe()
	aOut_r, aOut_w := io.Pipe()
	bOut_r, bOut_w := io.Pipe()

	a = &splitConn{r: aIn_r, w: aOut_w}
	b = &splitConn{r: bIn_r, w: bOut_w}
	return a, b, aIn_w, bIn_w, aOut_r, bOut_r
}

func (c *splitConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *splitConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *splitConn) Close() error {
	rerr := c.r.Close()
	werr := c.w.Close()
	if rerr != nil {
		return rerr
	}
	return werr
}
func (c *splitConn) CloseWrite() error {
	if c.writeDone {
		return nil
	}
	c.writeDone = true
	return c.w.Close()
}

func TestPipe_BidirectionalData(t *testing.T) {
	a, b, aIn, bIn, aOut, bOut := newSplitConnPair()

	pipeDone := make(chan error, 1)
	go func() { pipeDone <- socketbridge.Pipe(a, b) }()

	const fromA = "hello from A side"
	const fromB = "reply from B side"

	// Write to a's input should emerge from b's output.
	go func() {
		if _, err := aIn.Write([]byte(fromA)); err != nil {
			t.Errorf("writing to aIn: %v", err)
		}
		aIn.Close()
	}()

	got := make([]byte, len(fromA))
	if _, err := io.ReadFull(bOut, got); err != nil {
		t.Fatalf("reading from bOut: %v", err)
	}
	if string(got) != fromA {
		t.Errorf("bOut got %q, want %q", got, fromA)
	}

	// Write to b's input should emerge from a's output.
	go func() {
		if _, err := bIn.Write([]byte(fromB)); err != nil {
			t.Errorf("writing to bIn: %v", err)
		}
		bIn.Close()
	}()

	got2 := make([]byte, len(fromB))
	if _, err := io.ReadFull(aOut, got2); err != nil {
		t.Fatalf("reading from aOut: %v", err)
	}
	if string(got2) != fromB {
		t.Errorf("aOut got %q, want %q", got2, fromB)
	}

	if err := <-pipeDone; err != nil {
		t.Errorf("Pipe returned unexpected error: %v", err)
	}
}

func TestPipe_HalfClose(t *testing.T) {
	// Verify Pipe completes cleanly with no errors when both input sides are closed without
	// writing any data, EOF should propagate via CloseWrite to the other side.
	a, b, aIn, bIn, _, _ := newSplitConnPair()

	pipeDone := make(chan error, 1)
	go func() { pipeDone <- socketbridge.Pipe(a, b) }()

	aIn.Close()
	bIn.Close()

	if err := <-pipeDone; err != nil {
		t.Errorf("Pipe returned unexpected error: %v", err)
	}
}

