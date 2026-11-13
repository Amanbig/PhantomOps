package remediate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Nomad reverts Nomad service jobs to their previous stable version.
type Nomad struct {
	connection string
	base       string
	token      string
	http       *http.Client
}

// NewNomad creates a Nomad executor for the named connection.
func NewNomad(connection, baseURL, token string) *Nomad {
	return &Nomad{connection: connection, base: strings.TrimRight(baseURL, "/"), token: token,
		http: &http.Client{Timeout: 30 * time.Second}}
}

type nomadJob struct {
	ID      string `json:"ID"`
	Type    string `json:"Type"`
	Version uint64 `json:"Version"`
	Status  string `json:"Status"`
}

type nomadVersion struct {
	Version    uint64 `json:"Version"`
	Stable     bool   `json:"Stable"`
	SubmitTime int64  `json:"SubmitTime"`
	TaskGroups []struct {
		Name  string `json:"Name"`
		Tasks []struct {
			Name      string            `json:"Name"`
			Config    map[string]any    `json:"Config"`
			Env       map[string]string `json:"Env"`
			Resources struct {
				CPU      int `json:"CPU"`
				MemoryMB int `json:"MemoryMB"`
			} `json:"Resources"`
		} `json:"Tasks"`
	} `json:"TaskGroups"`
}

func (n *Nomad) do(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	u := n.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if n.token != "" {
		req.Header.Set("X-Nomad-Token", n.token)
	}
	resp, err := n.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, msg)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func nsQuery(ns string) url.Values { return url.Values{"namespace": {ns}} }

func nomadImage(v nomadVersion) string {
	for _, g := range v.TaskGroups {
		for _, t := range g.Tasks {
			if img, ok := t.Config["image"].(string); ok {
				return img
			}
		}
	}
	return "image unknown"
}

// Plan inspects the job and picks the latest stable earlier version.
func (n *Nomad) Plan(ctx context.Context, target string) (Plan, error) {
	ns, id, err := splitTarget(target)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Platform: "nomad", Connection: n.connection, Target: target}
	if !nomadName.MatchString(ns) || !nomadName.MatchString(id) {
		return p, fmt.Errorf("%q is not a valid Nomad namespace/job id", target)
	}

	var job nomadJob
	if err := n.do(ctx, http.MethodGet, "/v1/job/"+url.PathEscape(id), nsQuery(ns), nil, &job); err != nil {
		return p, fmt.Errorf("cannot read job %s: %w", target, err)
	}
	if job.Type != "service" {
		p.Reason = fmt.Sprintf("job type %q is not supported; only service jobs can be rolled back", job.Type)
		return p, nil
	}
	var vs struct {
		Versions []nomadVersion `json:"Versions"`
	}
	if err := n.do(ctx, http.MethodGet, "/v1/job/"+url.PathEscape(id)+"/versions", nsQuery(ns), nil, &vs); err != nil {
		return p, fmt.Errorf("cannot read job versions: %w", err)
	}

	var cur *nomadVersion
	var prev *nomadVersion
	for i := range vs.Versions {
		v := &vs.Versions[i]
		if v.Version == job.Version {
			cur = v
		}
		if v.Version < job.Version && v.Stable && (prev == nil || v.Version > prev.Version) {
			prev = v
		}
	}
	curImg := "image unknown"
	if cur != nil {
		curImg = nomadImage(*cur)
	}
	p.Current = fmt.Sprintf("version %d (%s)", job.Version, curImg)
	if prev == nil {
		p.Reason = "there is no earlier stable version to roll back to (a version counts as stable only after a successful deployment)"
		return p, nil
	}
	p.Previous = fmt.Sprintf("version %d (%s)", prev.Version, nomadImage(*prev))
	if cur != nil {
		if p.Changes = diffNomad(*cur, *prev); len(p.Changes) == 0 {
			p.Reason = fmt.Sprintf("versions %d and %d have identical task definitions; reverting would not change anything", job.Version, prev.Version)
			return p, nil
		}
	} else {
		p.Notes = append(p.Notes, "Could not read the current version, so the exact changes are unknown.")
	}
	p.Command = fmt.Sprintf("POST %s/v1/job/%s/revert?namespace=%s {\"JobVersion\": %d, \"EnforcePriorVersion\": %d}",
		n.base, id, ns, prev.Version, job.Version)
	p.Feasible = true
	p.fingerprint = fmt.Sprintf("%d>%d", job.Version, prev.Version)
	p.Notes = append(p.Notes,
		"Nomad registers the old job definition as a new version and replaces the running allocations.",
		"The revert is rejected by Nomad if the job was modified after this preview (EnforcePriorVersion).",
	)
	return p, nil
}

// Execute re-plans, reverts and waits for the new allocations to run.
func (n *Nomad) Execute(ctx context.Context, plan Plan) (Result, error) {
	if !plan.Feasible {
		return Result{}, errors.New("plan is not feasible: " + plan.Reason)
	}
	fresh, err := n.Plan(ctx, plan.Target)
	if err != nil {
		return Result{}, err
	}
	if !fresh.Feasible || fresh.fingerprint != plan.fingerprint {
		return Result{}, ErrStateChanged
	}
	ns, id, _ := splitTarget(plan.Target)
	var cur, prev uint64
	if _, err := fmt.Sscanf(plan.fingerprint, "%d>%d", &cur, &prev); err != nil {
		return Result{}, errors.New("internal error: bad plan fingerprint")
	}

	body := map[string]any{"JobID": id, "JobVersion": prev, "EnforcePriorVersion": cur}
	if err := n.do(ctx, http.MethodPost, "/v1/job/"+url.PathEscape(id)+"/revert", nsQuery(ns), body, nil); err != nil {
		return Result{}, fmt.Errorf("rollback failed: %w", err)
	}
	res := Result{Applied: true}

	// Wait for allocations of the new version to run.
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			res.Message = "Rollback applied; stopped waiting: " + ctx.Err().Error()
			return res, nil
		case <-time.After(3 * time.Second):
		}
		ok, detail := n.allocsHealthy(ctx, ns, id)
		if ok {
			res.Healthy = true
			res.Message = fmt.Sprintf("Reverted %s to version %d. %s", plan.Target, prev, detail)
			return res, nil
		}
	}
	res.Message = "Rollback applied, but the new allocations were not all running within 75 s. Check the job in Nomad."
	return res, nil
}

// allocsHealthy reports whether every wanted allocation of the job's current
// version is running.
func (n *Nomad) allocsHealthy(ctx context.Context, ns, id string) (bool, string) {
	var job nomadJob
	if n.do(ctx, http.MethodGet, "/v1/job/"+url.PathEscape(id), nsQuery(ns), nil, &job) != nil {
		return false, ""
	}
	var allocs []struct {
		JobVersion    uint64 `json:"JobVersion"`
		ClientStatus  string `json:"ClientStatus"`
		DesiredStatus string `json:"DesiredStatus"`
	}
	if n.do(ctx, http.MethodGet, "/v1/job/"+url.PathEscape(id)+"/allocations", nsQuery(ns), nil, &allocs) != nil {
		return false, ""
	}
	running, wanted := 0, 0
	for _, a := range allocs {
		if a.JobVersion != job.Version || a.DesiredStatus != "run" {
			continue
		}
		wanted++
		if a.ClientStatus == "running" {
			running++
		}
	}
	if wanted == 0 || running != wanted {
		return false, ""
	}
	return true, fmt.Sprintf("%d allocation(s) running on version %d.", running, job.Version)
}

type nomadTask struct {
	config map[string]any
	env    map[string]string
	cpu    int
	mem    int
}

func nomadTasks(v nomadVersion) map[string]nomadTask {
	out := map[string]nomadTask{}
	for _, g := range v.TaskGroups {
		for _, t := range g.Tasks {
			out[g.Name+"/"+t.Name] = nomadTask{config: t.Config, env: t.Env, cpu: t.Resources.CPU, mem: t.Resources.MemoryMB}
		}
	}
	return out
}

func anyStr(v any) string {
	if v == nil {
		return "(none)"
	}
	if s, ok := v.(string); ok {
		return short(s)
	}
	b, _ := json.Marshal(v)
	return short(string(b))
}

// diffNomad lists what changes when going from cur to prev. Environment
// values are never printed (only names), as they may be secrets.
func diffNomad(cur, prev nomadVersion) []string {
	var out []string
	pt := nomadTasks(prev)
	ct := nomadTasks(cur)
	keys := make([]string, 0, len(ct))
	for k := range ct {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := ct[k]
		p, ok := pt[k]
		if !ok {
			out = append(out, fmt.Sprintf("task %s: does not exist in the earlier version", k))
			continue
		}
		pre := "task " + k + ": "
		for _, f := range []string{"image", "command", "args"} {
			if anyStr(c.config[f]) != anyStr(p.config[f]) {
				out = append(out, fmt.Sprintf("%s%s %s → %s", pre, f, anyStr(c.config[f]), anyStr(p.config[f])))
			}
		}
		names := map[string]bool{}
		for n := range c.env {
			names[n] = true
		}
		for n := range p.env {
			names[n] = true
		}
		var ns []string
		for n := range names {
			ns = append(ns, n)
		}
		sort.Strings(ns)
		for _, n := range ns {
			cv, inC := c.env[n]
			pv, inP := p.env[n]
			switch {
			case inC && !inP:
				out = append(out, fmt.Sprintf("%senv %s will be removed", pre, n))
			case !inC && inP:
				out = append(out, fmt.Sprintf("%senv %s will be added", pre, n))
			case cv != pv:
				out = append(out, fmt.Sprintf("%senv %s will change value", pre, n))
			}
		}
		if c.mem != p.mem {
			out = append(out, fmt.Sprintf("%smemory %d MB → %d MB", pre, c.mem, p.mem))
		}
		if c.cpu != p.cpu {
			out = append(out, fmt.Sprintf("%scpu %d MHz → %d MHz", pre, c.cpu, p.cpu))
		}
	}
	return out
}
