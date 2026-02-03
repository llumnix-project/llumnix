package utils

import (
	"os"
	"strconv"
	"strings"

	"k8s.io/klog/v2"
)

// GetLimitMemory retrieves the system memory limit with fallback priority:
// 1. MEM_LIMIT environment variable - Explicit memory limit set by user
// 2. cgroup v2: /sys/fs/cgroup/memory.max - Container runtime memory limit (unified cgroup)
// 3. cgroup v1: /sys/fs/cgroup/memory/memory.limit_in_bytes - Container runtime memory limit (legacy cgroup)
// 4. System physical memory: /proc/meminfo MemTotal - Host physical memory
//
// Returns memory size in bytes, or 0 if all detection methods fail.
//
// References:
// - cgroup v2 memory controller: https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#memory
// - cgroup v1 memory controller: https://www.kernel.org/doc/Documentation/cgroup-v1/memory.txt
// - /proc/meminfo format: https://man7.org/linux/man-pages/man5/proc.5.html
func GetLimitMemory() int64 {
	// 1: Check MEM_LIMIT environment variable for explicit configuration
	if memLimitStr := os.Getenv("MEM_LIMIT"); memLimitStr != "" {
		if memLimit, err := strconv.ParseInt(memLimitStr, 10, 64); err == nil && memLimit > 0 {
			klog.Infof("Using MEM_LIMIT from environment: %d bytes", memLimit)
			return memLimit
		}
		klog.Warningf("Invalid MEM_LIMIT value: %s, falling back to cgroup/system memory", memLimitStr)
	}

	//  2: Try cgroup v2 memory limit (unified hierarchy)
	// The memory.max file contains either a numeric limit or "max" (unlimited)
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		limitStr := strings.TrimSpace(string(data))
		if limitStr != "max" {
			if limit, err := strconv.ParseInt(limitStr, 10, 64); err == nil && limit > 0 {
				klog.Infof("using cgroup v2 memory limit: %d bytes", limit)
				return limit
			}
		}
	}

	// 3: Try cgroup v1 memory limit (legacy hierarchy)
	// IMPORTANT: When unlimited, cgroup v1 returns a very large sentinel value
	// (typically 2^63-1 = 9223372036854775807 bytes ≈ 8 EB)
	// We filter out unreasonably large values (> 1PB) to detect unlimited scenarios
	if data, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		limitStr := strings.TrimSpace(string(data))
		if limit, err := strconv.ParseInt(limitStr, 10, 64); err == nil && limit > 0 {
			const maxReasonableLimit = 1 << 50 // 1 PB = 1125899906842624 bytes
			if limit < maxReasonableLimit {
				klog.Infof("using cgroup v1 memory limit: %d bytes", limit)
				return limit
			}
			klog.V(4).Infof("cgroup v1 memory limit too large (%d bytes), likely unlimited", limit)
		}
	}

	// 4: Fallback to system physical memory from /proc/meminfo
	// Format example: "MemTotal:       16384000 kB"
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if memKB, err := strconv.ParseInt(fields[1], 10, 64); err == nil && memKB > 0 {
						memBytes := memKB * 1024
						klog.Infof("using system physical memory: %d bytes", memBytes)
						return memBytes
					}
				}
				break
			}
		}
	}

	// All detection methods failed
	klog.Warning("Failed to determine memory limit in current env")
	return 0
}
