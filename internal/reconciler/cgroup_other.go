//go:build !linux

package reconciler

import "fmt"

type unsupportedCgroups struct{}

func NewHostCgroups(Config) CgroupAccess                         { return unsupportedCgroups{} }
func (unsupportedCgroups) Verify() error                         { return fmt.Errorf("Linux cgroup v2 is required") }
func (u unsupportedCgroups) SetOOMGroup(Candidate) (bool, error) { return false, u.Verify() }
func (u unsupportedCgroups) NeedsChange(Candidate) (bool, error) { return false, u.Verify() }
