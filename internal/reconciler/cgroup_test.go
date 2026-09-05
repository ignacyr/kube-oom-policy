package reconciler

import (
	"strings"
	"testing"
)

func cgroupCandidate() Candidate {
	return Candidate{PodUID: "12345678-1234-1234-1234-123456789abc", ContainerID: strings.Repeat("a", 64), QoSClass: "Burstable"}
}

func TestCgroupPath(t *testing.T) {
	c := cgroupCandidate()
	tests := []struct{ base, qos, want string }{
		{"kubepods.slice", "Guaranteed", "kubepods.slice/kubepods-pod12345678_1234_1234_1234_123456789abc.slice/cri-containerd-"},
		{"kubepods.slice", "Burstable", "kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod12345678_1234_1234_1234_123456789abc.slice/cri-containerd-"},
		{"kubelet.slice/kubelet-kubepods.slice", "BestEffort", "kubelet.slice/kubelet-kubepods.slice/kubelet-kubepods-besteffort.slice/kubelet-kubepods-besteffort-pod12345678_1234_1234_1234_123456789abc.slice/cri-containerd-"},
		{"kubepods", "Guaranteed", "kubepods/pod12345678-1234-1234-1234-123456789abc/"},
		{"kubepods", "Burstable", "kubepods/burstable/pod12345678-1234-1234-1234-123456789abc/"},
		{"custom/kubepods", "BestEffort", "custom/kubepods/besteffort/pod12345678-1234-1234-1234-123456789abc/"},
	}
	for _, tt := range tests {
		c.QoSClass = tt.qos
		got, err := cgroupPath(tt.base, c)
		want := tt.want + c.ContainerID
		if strings.HasSuffix(tt.base, ".slice") {
			want += ".scope"
		}
		if err != nil || got != want {
			t.Errorf("%s/%s: got %q, %v; want %q", tt.base, tt.qos, got, err, want)
		}
	}
}

func TestCgroupPathRejectsUntrustedIdentity(t *testing.T) {
	for _, mutate := range []func(*Candidate){
		func(c *Candidate) { c.PodUID = "../escape" },
		func(c *Candidate) { c.ContainerID = strings.Repeat("a", 63) },
		func(c *Candidate) { c.ContainerID = strings.Repeat("A", 64) },
		func(c *Candidate) { c.QoSClass = "" },
	} {
		c := cgroupCandidate()
		mutate(&c)
		if _, err := cgroupPath("kubepods.slice", c); err == nil {
			t.Fatal("unsafe identity accepted")
		}
	}
	for _, base := range []string{"../kubepods", "/kubepods", "x/../kubepods", "system.slice"} {
		if _, err := cgroupPath(base, cgroupCandidate()); err == nil {
			t.Fatalf("unsafe base %q accepted", base)
		}
	}
}

func TestControlValues(t *testing.T) {
	for _, value := range []string{"0", "0\n", "1", "1\n"} {
		if _, ok := exactControlValue([]byte(value)); !ok {
			t.Fatalf("rejected %q", value)
		}
	}
	for _, value := range []string{"", "00", "2", "1 ", "1\n0"} {
		if _, ok := exactControlValue([]byte(value)); ok {
			t.Fatalf("accepted %q", value)
		}
	}
	if !populatedOne([]byte("populated 1\nfrozen 0\n")) {
		t.Fatal("populated cgroup rejected")
	}
	for _, value := range []string{"populated 0\n", "populated 1\npopulated 1\n", "frozen 0\n", "populated 1 extra"} {
		if populatedOne([]byte(value)) {
			t.Fatalf("accepted %q", value)
		}
	}
}

func FuzzCgroupPath(f *testing.F) {
	f.Add("kubepods.slice", "12345678-1234-1234-1234-123456789abc", strings.Repeat("a", 64), "Burstable")
	f.Add("../kubepods", "../escape", "bad", "")
	f.Fuzz(func(t *testing.T, base, uid, id, qos string) {
		got, err := cgroupPath(base, Candidate{PodUID: uid, ContainerID: id, QoSClass: qos})
		if err == nil && (!validRelativePath(got) || !strings.Contains(got, id)) {
			t.Fatalf("unsafe path %q", got)
		}
	})
}
