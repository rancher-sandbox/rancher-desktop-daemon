// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package tail

import (
	"context"
	"fmt"
	"io"
)

// Stream prints the contents of filePath to writer. If follow is true, it waits
// for new lines at EOF; otherwise it returns at EOF. The stream stops when ctx
// is cancelled, when the writer returns an error (e.g. broken pipe), or (when
// follow is false) when EOF is reached.
func Stream(ctx context.Context, writer io.Writer, filePath string, follow bool) error {
	config := Config{
		ReOpen:        follow,
		Follow:        follow,
		CompleteLines: true,
	}
	t, err := Open(filePath, config)
	if err != nil {
		return err
	}

	if !follow {
		// Signal that we want to stop tailing at EOF.
		go func() {
			_ = t.StopAtEOF()
		}()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = t.Stop()
		case <-done:
		}
	}()

	var writeErr error
	for line := range t.Lines {
		if writeErr != nil {
			continue // drain Lines so tailFileSync can exit cleanly
		}
		if _, err := fmt.Fprintln(writer, line.Text); err != nil {
			writeErr = err
			t.Kill(err) // non-blocking; keep draining Lines so tailFileSync can finish
		}
	}
	return writeErr
}
