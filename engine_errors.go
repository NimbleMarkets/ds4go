package ds4

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var enginePIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bpid\s*[:=]?\s*([0-9]+)\b`),
	regexp.MustCompile(`(?i)\bprocess\s+([0-9]+)\b`),
	regexp.MustCompile(`(?i)\blocked\s+by\s+([0-9]+)\b`),
}

var processNameForPID = lookupProcessName

// EnrichEngineOpenError adds process names to ds4 engine-open errors that
// mention lock-holder PIDs.
func EnrichEngineOpenError(err error) error {
	if err == nil {
		return nil
	}
	pids := extractPIDs(err.Error())
	if len(pids) == 0 {
		return err
	}
	var details []string
	for _, pid := range pids {
		name, lookupErr := processNameForPID(pid)
		if lookupErr != nil || name == "" {
			details = append(details, fmt.Sprintf("pid %d: <process name unavailable>", pid))
			continue
		}
		details = append(details, fmt.Sprintf("pid %d: %s", pid, name))
	}
	return fmt.Errorf("%w\nLock holder details: %s", err, strings.Join(details, "; "))
}

func extractPIDs(msg string) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, re := range enginePIDPatterns {
		for _, match := range re.FindAllStringSubmatch(msg, -1) {
			if len(match) < 2 {
				continue
			}
			pid, err := strconv.Atoi(match[1])
			if err != nil || pid <= 0 {
				continue
			}
			if _, ok := seen[pid]; ok {
				continue
			}
			seen[pid] = struct{}{}
			out = append(out, pid)
		}
	}
	return out
}

func lookupProcessName(pid int) (string, error) {
	if runtime.GOOS == "windows" {
		return lookupProcessNameWindows(pid)
	}
	if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		if name := strings.TrimSpace(string(comm)); name != "" {
			return name, nil
		}
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func lookupProcessNameWindows(pid int) (string, error) {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(out))
	if line == "" || strings.Contains(line, "No tasks") {
		return "", fmt.Errorf("pid %d not found", pid)
	}
	fields := strings.Split(line, ",")
	if len(fields) == 0 {
		return "", fmt.Errorf("unexpected tasklist output")
	}
	return strings.Trim(fields[0], `"`), nil
}
