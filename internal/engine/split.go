package engine

import (
	"sort"

	"PhantomOps/internal/model"
)

// isRootSignal marks events that are a failure in their own right (as opposed
// to a downstream symptom such as upstream 5xx errors).
func isRootSignal(e model.Event) bool {
	return isStartFailure(e) || isOOM(e) || isCrash(e) || isUnhealthy(e) || isMemoryPressure(e) || isDepExhaustion(e) ||
		(e.Source == model.SourceSSH && isSymptom(e))
}

// splitByService divides a time-contiguous cluster into incidents.
//
// Events are grouped into components of related subjects. Components with a
// root-signal failure become separate incidents (two unrelated services
// failing at the same time are two incidents). Components with only
// downstream symptoms or changes are attached to the most recent preceding
// root-cause component, which keeps cross-service chains such as
// "api leaks memory -> proxy returns 502" together.
func splitByService(events []model.Event) [][]model.Event {
	n := len(events)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if events[i].Subject == events[j].Subject || related(events[i].Subject, events[j].Subject) {
				parent[find(i)] = find(j)
			}
		}
	}

	type comp struct {
		events  []model.Event
		root    bool
		firstSy int64 // unix nano of the first symptom, 0 if none
	}
	byRoot := map[int]*comp{}
	var order []int
	for i, ev := range events {
		r := find(i)
		c := byRoot[r]
		if c == nil {
			c = &comp{}
			byRoot[r] = c
			order = append(order, r)
		}
		c.events = append(c.events, ev)
		if isRootSignal(ev) {
			c.root = true
		}
		if isSymptom(ev) && c.firstSy == 0 {
			c.firstSy = ev.Time.UnixNano()
		}
	}

	var roots, others []*comp
	for _, r := range order {
		if c := byRoot[r]; c.root {
			roots = append(roots, c)
		} else {
			others = append(others, c)
		}
	}
	if len(roots) == 0 {
		return [][]model.Event{events} // nothing to separate on
	}

	for _, o := range others {
		ref := o.events[0].Time.UnixNano()
		if o.firstSy != 0 {
			ref = o.firstSy
		}
		// Attach to the root component whose first symptom most recently
		// precedes this one; otherwise the earliest root component.
		best := roots[0]
		for _, r := range roots {
			if r.firstSy != 0 && r.firstSy <= ref && (best.firstSy > ref || r.firstSy > best.firstSy) {
				best = r
			}
		}
		best.events = append(best.events, o.events...)
	}

	out := make([][]model.Event, 0, len(roots))
	for _, r := range roots {
		sort.SliceStable(r.events, func(i, j int) bool { return r.events[i].Time.Before(r.events[j].Time) })
		out = append(out, r.events)
	}
	return out
}
