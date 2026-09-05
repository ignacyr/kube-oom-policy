//go:build linux

package reconciler

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenBeneathRejectsTraversalAndSymlinks(t *testing.T) {
	root, victim := t.TempDir(), t.TempDir()
	file := filepath.Join(victim, "memory.oom.group")
	if err := os.WriteFile(file, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	rootFD, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(rootFD)
	for _, p := range []string{"../memory.oom.group", "link/memory.oom.group", "/etc/passwd"} {
		fd, err := openBeneath(rootFD, p, unix.O_RDWR)
		if err == nil {
			unix.Close(fd)
			t.Fatalf("unsafe path %q opened", p)
		}
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "1" {
		t.Fatalf("victim changed: %q, %v", got, err)
	}
}

func TestHostCgroupsRejectOrdinaryFilesystem(t *testing.T) {
	h := &hostCgroups{root: t.TempDir(), bases: []string{"kubepods.slice"}, desired: '0'}
	if err := h.Verify(); err == nil {
		t.Fatal("ordinary filesystem accepted")
	}
	h.bases = []string{"kubepods.slice"}
	p, _ := cgroupPath(h.bases[0], cgroupCandidate())
	dir := filepath.Join(h.root, p)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "memory.oom.group")
	if err := os.WriteFile(target, []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SetOOMGroup(cgroupCandidate()); err == nil {
		t.Fatal("ordinary file reached writer")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "1" {
		t.Fatalf("target changed: %q, %v", got, err)
	}
}

func TestReadSmallRejectsOversize(t *testing.T) {
	p := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(p, []byte("123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if _, err := readSmall(fd, 8); err == nil {
		t.Fatal("oversize control accepted")
	}
}
