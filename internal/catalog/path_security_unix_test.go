//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package catalog

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRegularWithinRootRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "retarget.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFO test unavailable: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		file, _, _, err := openRegularWithinRoot(root, fifo)
		if file != nil {
			file.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO was accepted as a regular library file")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a FIFO blocked instead of rejecting it")
	}
}
