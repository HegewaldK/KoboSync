package kobo

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFindUnmountedAndMount(t *testing.T) {
	dir := t.TempDir()
	byLabel = filepath.Join(dir, "by-label")
	os.MkdirAll(byLabel, 0o755)
	if FindUnmounted() != "" {
		t.Fatal("found a Kobo without the label link")
	}
	// The label link points at the device node, like udev's does.
	dev := filepath.Join(dir, "sdz")
	os.WriteFile(dev, nil, 0o644)
	os.Symlink(dev, filepath.Join(byLabel, Label))
	if got := FindUnmounted(); got != dev {
		t.Fatalf("FindUnmounted = %q, want %q", got, dev)
	}

	var called []string
	mountCmd = func(d string) ([]byte, error) {
		called = append(called, d)
		if d == "/dev/sdfail" {
			return []byte("Error mounting /dev/sdfail: Not authorized"), errors.New("exit 1")
		}
		return []byte("Mounted " + d + " at /run/media/me/KOBOeReader"), nil
	}
	if err := Mount("/dev/sdz"); err != nil {
		t.Errorf("Mount: %v", err)
	}
	if err := Mount("/dev/sdfail"); err == nil || err.Error() != "mount /dev/sdfail: Error mounting /dev/sdfail: Not authorized" {
		t.Errorf("Mount error: %v", err)
	}
	if err := Mount("../etc/passwd"); err == nil {
		t.Error("Mount accepted a non-device path")
	}
	if len(called) != 2 {
		t.Errorf("udisksctl called for %v", called)
	}
}
