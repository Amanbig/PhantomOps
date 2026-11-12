package ssh

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Snapshot is the current state of an SSH host.
type Snapshot struct {
	Type      string     `json:"type"` // "ssh"
	TakenAt   time.Time  `json:"takenAt"`
	Host      string     `json:"host"`
	Hostname  string     `json:"hostname"`
	OS        string     `json:"os"`
	Kernel    string     `json:"kernel"`
	UptimeSec int64      `json:"uptimeSec"`
	CPUs      int        `json:"cpus"`
	Load      [3]float64 `json:"load"`
	Memory    MemInfo    `json:"memory"`
	Disks     []Disk     `json:"disks"`
	Failed    []string   `json:"failedUnits"`
	Ports     []Port     `json:"ports"`
	Processes []Process  `json:"processes"`
	// Systemd is false when the host does not run systemd (unit status unknown).
	Systemd bool     `json:"systemd"`
	Partial []string `json:"partial"`
}

// MemInfo is memory and swap in bytes.
type MemInfo struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
	SwapTotal uint64 `json:"swapTotal"`
	SwapFree  uint64 `json:"swapFree"`
}

// Disk is one mounted filesystem.
type Disk struct {
	Mount      string `json:"mount"`
	Device     string `json:"device"`
	SizeBytes  uint64 `json:"sizeBytes"`
	UsedBytes  uint64 `json:"usedBytes"`
	UsePercent int    `json:"usePercent"`
}

// Port is an expected TCP port and whether something listens on it.
type Port struct {
	Port      int  `json:"port"`
	Listening bool `json:"listening"`
}

// Process is one of the busiest processes.
type Process struct {
	PID     int     `json:"pid"`
	User    string  `json:"user"`
	Command string  `json:"command"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
}

// Additional read-only commands used only by the dashboard. Like the
// collector's, they are fixed strings: nothing user-supplied is executed.
const (
	cmdIdentity = "hostname; uname -sr; cat /proc/uptime; (. /etc/os-release 2>/dev/null && echo \"$PRETTY_NAME\") || uname -o"
	cmdProcs    = "ps -eo pid=,user=,pcpu=,pmem=,comm= 2>/dev/null | head -400"
	maxProcs    = 8
)

// Snapshot implements collector.Snapshotter over a single SSH connection.
func (c *Collector) Snapshot(ctx context.Context) (any, error) {
	run, closeFn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("ssh connect %s: %w", c.cfg.Addr, err)
	}
	defer closeFn()

	snap := Snapshot{Type: "ssh", TakenAt: c.now(), Host: c.cfg.Addr, Disks: []Disk{}, Failed: []string{},
		Ports: []Port{}, Processes: []Process{}, Partial: []string{}}
	exec := func(name, cmd string) (string, bool) {
		out, code, err := run(ctx, cmd)
		if err != nil {
			snap.Partial = append(snap.Partial, fmt.Sprintf("%s: %v", name, err))
			return "", false
		}
		return out, code == 0 || out != ""
	}

	if out, ok := exec("identity", cmdIdentity); ok {
		ls := lines(out)
		if len(ls) > 0 {
			snap.Hostname = ls[0]
		}
		if len(ls) > 1 {
			snap.Kernel = ls[1]
		}
		if len(ls) > 2 {
			if f := strings.Fields(ls[2]); len(f) > 0 {
				up, _ := strconv.ParseFloat(f[0], 64)
				snap.UptimeSec = int64(up)
			}
		}
		if len(ls) > 3 {
			snap.OS = ls[3]
		}
	}
	if out, ok := exec("load", cmdLoad); ok {
		ls := lines(out)
		if len(ls) >= 2 {
			f := strings.Fields(ls[0])
			for i := 0; i < 3 && i < len(f); i++ {
				snap.Load[i], _ = strconv.ParseFloat(f[i], 64)
			}
			snap.CPUs, _ = strconv.Atoi(ls[len(ls)-1])
		}
	}
	if out, ok := exec("memory", cmdMem); ok {
		for _, l := range lines(out) {
			f := strings.Fields(l)
			if len(f) < 2 {
				continue
			}
			kb, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				continue
			}
			switch strings.TrimSuffix(f[0], ":") {
			case "MemTotal":
				snap.Memory.Total = kb * 1024
			case "MemAvailable":
				snap.Memory.Available = kb * 1024
			case "SwapTotal":
				snap.Memory.SwapTotal = kb * 1024
			case "SwapFree":
				snap.Memory.SwapFree = kb * 1024
			}
		}
	}
	if out, ok := exec("disk", cmdDisk); ok {
		for i, l := range lines(out) {
			f := strings.Fields(l)
			if i == 0 || len(f) < 6 || ignoredFS[f[0]] || strings.HasPrefix(f[0], "overlay") && f[5] != "/" {
				continue
			}
			size, _ := strconv.ParseUint(f[1], 10, 64)
			used, _ := strconv.ParseUint(f[2], 10, 64)
			pct, _ := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
			snap.Disks = append(snap.Disks, Disk{Mount: strings.Join(f[5:], " "), Device: f[0],
				SizeBytes: size * 1024, UsedBytes: used * 1024, UsePercent: pct})
		}
		sort.SliceStable(snap.Disks, func(i, j int) bool { return snap.Disks[i].UsePercent > snap.Disks[j].UsePercent })
	}
	if out, code, err := run(ctx, cmdUnits); err == nil && (code == 0 || out != "") {
		snap.Systemd = true
		for _, l := range lines(out) {
			if f := strings.Fields(strings.TrimPrefix(l, "●")); len(f) > 0 {
				snap.Failed = append(snap.Failed, f[0])
			}
		}
	}
	if len(c.cfg.ExpectedPorts) > 0 {
		if out, ok := exec("ports", cmdPorts); ok {
			listening := listeningPorts(out)
			for _, p := range c.cfg.ExpectedPorts {
				snap.Ports = append(snap.Ports, Port{Port: p, Listening: listening[p]})
			}
		}
	}
	if out, ok := exec("processes", cmdProcs); ok {
		for _, l := range lines(out) {
			f := strings.Fields(l)
			if len(f) < 5 {
				continue
			}
			pid, err := strconv.Atoi(f[0])
			if err != nil {
				continue
			}
			cpu, _ := strconv.ParseFloat(f[2], 64)
			mem, _ := strconv.ParseFloat(f[3], 64)
			snap.Processes = append(snap.Processes, Process{PID: pid, User: f[1], CPU: cpu, Mem: mem, Command: strings.Join(f[4:], " ")})
		}
		// Sort here rather than with `ps --sort`, which BusyBox ignores.
		sort.SliceStable(snap.Processes, func(i, j int) bool {
			a, b := snap.Processes[i], snap.Processes[j]
			if a.CPU != b.CPU {
				return a.CPU > b.CPU
			}
			return a.Mem > b.Mem
		})
		if len(snap.Processes) > maxProcs {
			snap.Processes = snap.Processes[:maxProcs]
		}
	}
	return snap, nil
}

// listeningPorts parses `ss -H -ltn` or `netstat -ltn` (local address is the
// 4th column in both).
func listeningPorts(out string) map[int]bool {
	m := map[int]bool{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		if i := strings.LastIndex(f[3], ":"); i >= 0 {
			if p, err := strconv.Atoi(f[3][i+1:]); err == nil {
				m[p] = true
			}
		}
	}
	return m
}
