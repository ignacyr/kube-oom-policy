//go:build linux

package reconciler

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type hostCgroups struct {
	root    string
	bases   []string
	desired byte
}

func NewHostCgroups(cfg Config) CgroupAccess {
	return &hostCgroups{root: cfg.CgroupRoot, desired: cfg.OOMGroup}
}

func (h *hostCgroups) openRoot() (int, error) {
	fd, err := unix.Open(h.root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, fmt.Errorf("open host cgroup mount: %w", err)
	}
	var stat unix.Statfs_t
	if err = unix.Fstatfs(fd, &stat); err == nil {
		if stat.Type != unix.CGROUP2_SUPER_MAGIC {
			err = fmt.Errorf("host mount is not cgroup v2")
		} else if stat.Flags&unix.ST_RDONLY != 0 {
			err = fmt.Errorf("host cgroup mount is read-only")
		}
	}
	if err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func (h *hostCgroups) Verify() error {
	h.bases = nil
	root, err := h.openRoot()
	if err != nil {
		return err
	}
	defer unix.Close(root)
	// Verify openat2 support before accepting work.
	fd, err := openBeneath(root, "cgroup.controllers", unix.O_RDONLY)
	if err != nil {
		return fmt.Errorf("open cgroup v2 controllers with openat2: %w", err)
	}
	unix.Close(fd)
	// Locate Kubernetes roots once per cycle, without scanning their workloads.
	// This also handles a custom kubelet slice such as kubelet-kubepods.slice.
	err = filepath.WalkDir(h.root, func(p string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil // unrelated cgroup disappeared during discovery
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() || p == h.root {
			return nil
		}
		name := entry.Name()
		if name == "kubepods" || name == "kubepods.slice" || strings.HasSuffix(name, "-kubepods.slice") {
			rel, err := filepath.Rel(h.root, p)
			if err != nil {
				return err
			}
			h.bases = append(h.bases, filepath.ToSlash(rel))
			return filepath.SkipDir
		}
		if strings.HasSuffix(name, ".scope") {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		h.bases = nil
		return fmt.Errorf("discover Kubernetes cgroup roots: %w", err)
	}
	if len(h.bases) == 0 {
		return fmt.Errorf("no Kubernetes cgroup root found")
	}
	return nil
}

func (h *hostCgroups) openTarget(c Candidate, flags int) (int, error) {
	if h.desired != '0' && h.desired != '1' {
		return -1, fmt.Errorf("OOM group value must be 0 or 1")
	}
	if len(h.bases) == 0 {
		return -1, fmt.Errorf("cgroup discovery has not succeeded")
	}
	root, err := h.openRoot()
	if err != nil {
		return -1, err
	}
	defer unix.Close(root)
	// Pin the directory once. All subsequent controls are opened relative to it.
	parent := -1
	defer func() {
		if parent >= 0 {
			unix.Close(parent)
		}
	}()
	for _, base := range h.bases {
		p, err := cgroupPath(base, c)
		if err != nil {
			return -1, err
		}
		fd, err := openBeneath(root, p, unix.O_PATH|unix.O_DIRECTORY)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return -1, fmt.Errorf("open container cgroup: %w", err)
		}
		if parent >= 0 {
			unix.Close(fd)
			return -1, fmt.Errorf("container maps to multiple cgroups")
		}
		parent = fd
	}
	if parent < 0 {
		return -1, fmt.Errorf("container cgroup is not present")
	}
	events, err := openBeneath(parent, "cgroup.events", unix.O_RDONLY)
	if err != nil {
		return -1, err
	}
	defer unix.Close(events)
	contents, err := readSmall(events, 4096)
	if err != nil || !populatedOne(contents) {
		return -1, fmt.Errorf("container cgroup is no longer populated")
	}
	return openBeneath(parent, "memory.oom.group", flags)
}

func (h *hostCgroups) NeedsChange(c Candidate) (bool, error) {
	target, err := h.openTarget(c, unix.O_RDONLY)
	if err != nil {
		return false, err
	}
	defer unix.Close(target)
	current, err := readControl(target)
	return current != h.desired, err
}

func (h *hostCgroups) SetOOMGroup(c Candidate) (bool, error) {
	target, err := h.openTarget(c, unix.O_RDWR)
	if err != nil {
		return false, err
	}
	defer unix.Close(target)
	current, err := readControl(target)
	if err != nil {
		return false, err
	}
	if current == h.desired {
		return false, nil
	}
	if _, err := unix.Seek(target, 0, 0); err != nil {
		return false, err
	}
	n, err := unix.Write(target, []byte{h.desired})
	if err != nil || n != 1 {
		return false, fmt.Errorf("write memory.oom.group: bytes=%d error=%v", n, err)
	}
	current, err = readControl(target)
	if err != nil {
		return true, fmt.Errorf("read memory.oom.group after write: %w", err)
	}
	if current != h.desired {
		return true, fmt.Errorf("memory.oom.group readback did not verify %c", h.desired)
	}
	return true, nil
}

func readControl(fd int) (byte, error) {
	contents, err := readSmall(fd, 8)
	if err != nil {
		return 0, err
	}
	value, ok := exactControlValue(contents)
	if !ok {
		return 0, fmt.Errorf("unexpected memory.oom.group value")
	}
	return value, nil
}

func openBeneath(root int, relative string, flags int) (int, error) {
	if !validRelativePath(relative) {
		return -1, fmt.Errorf("unsafe relative cgroup path")
	}
	return unix.Openat2(root, relative, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS |
			unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_XDEV,
	})
}

func readSmall(fd, capacity int) ([]byte, error) {
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return nil, err
	}
	buffer := make([]byte, capacity+1)
	total := 0
	for total < len(buffer) {
		n, err := unix.Read(fd, buffer[total:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		total += n
		if n == 0 {
			break
		}
	}
	if total > capacity {
		return nil, fmt.Errorf("cgroup control exceeds %d bytes", capacity)
	}
	return buffer[:total], nil
}
