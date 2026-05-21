package models

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// GetProcessName returns the executable name of the process with the given PID,
// or "unknown" if it cannot be determined.
func GetProcessName(pid int) string {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
		if out, err := cmd.Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.Split(line, ",")
				if len(parts) > 0 {
					name := strings.Trim(parts[0], "\"")
					if name != "" && !strings.Contains(name, "No tasks are running") {
						return name
					}
				}
			}
		}
		return "unknown"
	}

	if runtime.GOOS == "linux" {
		commPath := fmt.Sprintf("/proc/%d/comm", pid)
		if data, err := os.ReadFile(commPath); err == nil {
			return strings.TrimSpace(string(data))
		}
		cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
		if data, err := os.ReadFile(cmdlinePath); err == nil {
			parts := strings.Split(string(data), "\x00")
			if len(parts) > 0 && parts[0] != "" {
				return filepath.Base(parts[0])
			}
		}
	}

	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=")
	if out, err := cmd.Output(); err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return filepath.Base(name)
		}
	}

	return "unknown"
}

// AcquireEngineRunLock attempts to acquire the run lock file for a given model path.
// If the lock is already held by another process, it returns a descriptive error
// indicating the holding process PID and name.
func AcquireEngineRunLock(modelPath string) (*FileLock, error) {
	if modelPath == "" {
		return nil, nil
	}
	lockPath := modelPath + ".run.lock"
	runLock, err := TryLock(lockPath)
	if err != nil {
		if err == ErrLocked {
			pid, holderErr := GetLockHolder(lockPath)
			if holderErr == nil && pid > 0 {
				name := GetProcessName(pid)
				return nil, fmt.Errorf("ds4_engine_open: engine lock held by process %d (%s)", pid, name)
			}
			return nil, fmt.Errorf("ds4_engine_open: engine lock held by another process")
		}
		return nil, fmt.Errorf("failed to acquire engine run lock: %w", err)
	}
	return runLock, nil
}
