package reconciler

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

type CgroupAccess interface {
	Verify() error
	NeedsChange(Candidate) (bool, error)
	SetOOMGroup(Candidate) (bool, error)
}

var (
	cgroupContainerID = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cgroupPodUID      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// Only the container leaf is eligible. Pod and QoS controls stay untouched.
func cgroupPath(base string, c Candidate) (string, error) {
	if !cgroupContainerID.MatchString(c.ContainerID) || !cgroupPodUID.MatchString(c.PodUID) || !validRelativePath(base) {
		return "", fmt.Errorf("invalid cgroup base, container ID or Pod UID")
	}
	var qos string
	switch c.QoSClass {
	case "Guaranteed":
	case "Burstable":
		qos = "burstable"
	case "BestEffort":
		qos = "besteffort"
	default:
		return "", fmt.Errorf("unsupported Pod QoS class %q", c.QoSClass)
	}
	name := path.Base(base)
	if name == "kubepods.slice" || strings.HasSuffix(name, "-kubepods.slice") {
		slice := strings.TrimSuffix(name, ".slice")
		if qos != "" {
			slice += "-" + qos
			base += "/" + slice + ".slice"
		}
		return base + "/" + slice + "-pod" + strings.ReplaceAll(c.PodUID, "-", "_") + ".slice/cri-containerd-" + c.ContainerID + ".scope", nil
	}
	if name != "kubepods" {
		return "", fmt.Errorf("unsupported Kubernetes cgroup root")
	}
	if qos != "" {
		base += "/" + qos
	}
	return base + "/pod" + c.PodUID + "/" + c.ContainerID, nil
}

func validRelativePath(value string) bool {
	return value != "" && value != "." && !strings.HasPrefix(value, "/") &&
		path.Clean(value) == value && !strings.ContainsAny(value, "\\\x00") &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func exactControlValue(value []byte) (byte, bool) {
	if len(value) == 2 && value[1] == '\n' {
		value = value[:1]
	}
	if len(value) == 1 && (value[0] == '0' || value[0] == '1') {
		return value[0], true
	}
	return 0, false
}

func populatedOne(value []byte) bool {
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(value)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return false
		}
		if fields[0] == "populated" {
			if found || fields[1] != "1" {
				return false
			}
			found = true
		}
	}
	return found
}
