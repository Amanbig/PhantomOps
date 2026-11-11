package ssh

import (
	"fmt"
	"strconv"
	"strings"

	"PhantomOps/internal/model"
)

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// parseLoad expects /proc/loadavg on the first line and nproc on the last.
func (c *Collector) parseLoad(out string) []model.Event {
	ls := lines(out)
	if len(ls) < 2 {
		return nil
	}
	f := strings.Fields(ls[0])
	if len(f) < 3 {
		return nil
	}
	load1, err1 := strconv.ParseFloat(f[0], 64)
	load5, err2 := strconv.ParseFloat(f[1], 64)
	cpus, err3 := strconv.Atoi(ls[len(ls)-1])
	if err1 != nil || err2 != nil || err3 != nil || cpus <= 0 {
		return nil
	}
	ratio := load1 / float64(cpus)
	if ratio < 1 {
		return nil
	}
	sev := model.SeverityWarning
	if ratio >= 2 {
		sev = model.SeverityCritical
	}
	return []model.Event{c.event(model.KindResource, sev, "High system load",
		fmt.Sprintf("Load average is %.2f (1m) / %.2f (5m) on %d CPUs (%.1fx).", load1, load5, cpus, ratio))}
}

func (c *Collector) parseMemory(out string) []model.Event {
	kb := map[string]float64{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(f[1], 64)
		if err != nil {
			continue
		}
		kb[strings.TrimSuffix(f[0], ":")] = v
	}
	var evs []model.Event
	if total, avail := kb["MemTotal"], kb["MemAvailable"]; total > 0 && avail > 0 {
		pct := avail / total * 100
		if pct < 10 {
			sev := model.SeverityWarning
			if pct < 5 {
				sev = model.SeverityCritical
			}
			evs = append(evs, c.event(model.KindResource, sev, "Low available memory",
				fmt.Sprintf("Only %.0f MB of %.0f MB memory is available (%.1f%%).", avail/1024, total/1024, pct)))
		}
	}
	if st, sf := kb["SwapTotal"], kb["SwapFree"]; st > 0 {
		used := (st - sf) / st * 100
		if used >= 50 {
			sev := model.SeverityWarning
			if used >= 80 {
				sev = model.SeverityCritical
			}
			evs = append(evs, c.event(model.KindResource, sev, "Heavy swap usage",
				fmt.Sprintf("%.0f%% of swap is in use (%.0f MB of %.0f MB).", used, (st-sf)/1024, st/1024)))
		}
	}
	return evs
}

var ignoredFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "shm": true, "udev": true, "none": true,
	"efivarfs": true, "cgroup": true, "cgroup2": true,
}

func (c *Collector) parseDisk(out string) []model.Event {
	var evs []model.Event
	for i, l := range lines(out) {
		if i == 0 { // header
			continue
		}
		f := strings.Fields(l)
		if len(f) < 6 || ignoredFS[f[0]] {
			continue
		}
		pct, err := strconv.Atoi(strings.TrimSuffix(f[4], "%"))
		if err != nil || pct < 85 {
			continue
		}
		sev := model.SeverityWarning
		if pct >= 95 {
			sev = model.SeverityCritical
		}
		mount := strings.Join(f[5:], " ")
		evs = append(evs, c.event(model.KindResource, sev, "Disk almost full",
			fmt.Sprintf("%s (%s) is %d%% full.", mount, f[0], pct)))
	}
	return evs
}

func (c *Collector) parseFailedUnits(out string) []model.Event {
	var evs []model.Event
	for _, l := range lines(out) {
		f := strings.Fields(strings.TrimPrefix(l, "●"))
		if len(f) == 0 {
			continue
		}
		evs = append(evs, c.event(model.KindDegraded, model.SeverityCritical, "systemd unit failed",
			fmt.Sprintf("Unit %s is in a failed state.", f[0])))
	}
	return evs
}

func (c *Collector) parseOOM(out string) []model.Event {
	ls := lines(out)
	if len(ls) == 0 {
		return nil
	}
	for i, l := range ls {
		if len(l) > 200 {
			ls[i] = l[:200] + "…"
		}
	}
	return []model.Event{c.event(model.KindResource, model.SeverityCritical, "Kernel OOM killer activity",
		strings.Join(ls, "\n"))}
}

// parsePorts handles both `ss -H -ltn` and `netstat -ltn`: in both, the local
// address is the 4th column.
func (c *Collector) parsePorts(out string) []model.Event {
	listening := map[int]bool{}
	for _, l := range lines(out) {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		addr := f[3]
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			continue
		}
		if p, err := strconv.Atoi(addr[i+1:]); err == nil {
			listening[p] = true
		}
	}
	var evs []model.Event
	for _, p := range c.cfg.ExpectedPorts {
		if !listening[p] {
			evs = append(evs, c.event(model.KindDegraded, model.SeverityCritical, "Port not listening",
				fmt.Sprintf("Nothing is listening on TCP port %d.", p)))
		}
	}
	return evs
}
