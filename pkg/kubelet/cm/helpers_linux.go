/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	libcontainercgroups "github.com/opencontainers/runc/libcontainer/cgroups"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/kubelet/qos"
)

// ResourceConfigForPod takes the input pod and outputs the cgroup resource config.
func ResourceConfigForPod(pod *api.Pod) *ResourceConfig {
	// sum requests and limits, track if limits were applied for each resource.
	cpuRequests := int64(0)
	cpuLimits := int64(0)
	memoryLimits := int64(0)
	memoryLimitsDeclared := true
	cpuLimitsDeclared := true
	for _, container := range pod.Spec.Containers {
		cpuRequests += container.Resources.Requests.Cpu().MilliValue()
		cpuLimits += container.Resources.Limits.Cpu().MilliValue()
		if container.Resources.Limits.Cpu().IsZero() {
			cpuLimitsDeclared = false
		}
		memoryLimits += container.Resources.Limits.Memory().Value()
		if container.Resources.Limits.Memory().IsZero() {
			memoryLimitsDeclared = false
		}
	}

	// convert to CFS values
	cpuShares := MilliCPUToShares(cpuRequests)
	cpuQuota, cpuPeriod := MilliCPUToQuota(cpuLimits)

	// determine the qos class
	qosClass := qos.GetPodQOS(pod)

	// build the result
	result := &ResourceConfig{}
	if qosClass == qos.Guaranteed {
		result.CpuShares = &cpuShares
		result.CpuQuota = &cpuQuota
		result.CpuPeriod = &cpuPeriod
		result.Memory = &memoryLimits
	} else if qosClass == qos.Burstable {
		result.CpuShares = &cpuShares
		if cpuLimitsDeclared {
			result.CpuQuota = &cpuQuota
			result.CpuPeriod = &cpuPeriod
		}
		if memoryLimitsDeclared {
			result.Memory = &memoryLimits
		}
	} else {
		shares := int64(MinShares)
		result.CpuShares = &shares
	}
	return result
}

// GetCgroupSubsystems returns information about the mounted cgroup subsystems
func GetCgroupSubsystems() (*CgroupSubsystems, error) {
	// get all cgroup mounts.
	allCgroups, err := libcontainercgroups.GetCgroupMounts(true)
	if err != nil {
		return &CgroupSubsystems{}, err
	}
	if len(allCgroups) == 0 {
		return &CgroupSubsystems{}, fmt.Errorf("failed to find cgroup mounts")
	}
	mountPoints := make(map[string]string, len(allCgroups))
	for _, mount := range allCgroups {
		for _, subsystem := range mount.Subsystems {
			mountPoints[subsystem] = mount.Mountpoint
		}
	}
	return &CgroupSubsystems{
		Mounts:      allCgroups,
		MountPoints: mountPoints,
	}, nil
}

// getCgroupProcs takes a cgroup directory name as an argument
// reads through the cgroup's procs file and returns a list of tgid's.
// It returns an empty list if a procs file doesn't exists
func getCgroupProcs(dir string) ([]int, error) {
	procsFile := filepath.Join(dir, "cgroup.procs")
	f, err := os.Open(procsFile)
	if err != nil {
		if os.IsNotExist(err) {
			// The procsFile does not exist, So no pids attached to this directory
			return []int{}, nil
		}
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	out := []int{}
	for s.Scan() {
		if t := s.Text(); t != "" {
			pid, err := strconv.Atoi(t)
			if err != nil {
				return nil, fmt.Errorf("unexpected line in %v; could not convert to pid: %v", procsFile, err)
			}
			out = append(out, pid)
		}
	}
	return out, nil
}
