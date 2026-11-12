package remediate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"PhantomOps/internal/kubeconfig"
)

// Runner executes kubectl with the given arguments. It is a seam for tests.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Kube rolls Kubernetes Deployments back using kubectl, so any kubeconfig
// auth method kubectl supports works.
type Kube struct {
	connection  string
	kubeContext string
	kubeconfig  string
	run         Runner
}

// NewKube creates a Kubernetes executor for the named connection. kubeconfig
// may be empty (it is then resolved like the collector does).
func NewKube(connection, kubeContext, kubeconfigPath string) *Kube {
	k := &Kube{connection: connection, kubeContext: kubeContext, kubeconfig: kubeconfigPath}
	k.run = k.kubectl
	return k
}

// NewKubeWithRunner is NewKube with a custom runner (tests).
func NewKubeWithRunner(connection string, r Runner) *Kube {
	return &Kube{connection: connection, run: r}
}

func (k *Kube) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	if err := kubeconfig.Check(k.kubeconfig); err != nil {
		return nil, err
	}
	if k.kubeContext != "" {
		args = append([]string{"--context", k.kubeContext}, args...)
	}
	cmd := kubeconfig.Command(k.kubeconfig, func() *exec.Cmd { return exec.CommandContext(ctx, "kubectl", args...) })
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, kubeconfig.Explain(errb.String()))
	}
	return out.Bytes(), nil
}

func (k *Kube) displayCommand(ns, name string, rev int) string {
	ctx := ""
	if k.kubeContext != "" {
		ctx = "--context " + k.kubeContext + " "
	}
	return fmt.Sprintf("kubectl %s-n %s rollout undo deployment/%s --to-revision=%d", ctx, ns, name, rev)
}

type deploymentDoc struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// Plan inspects the Deployment and its revision history.
func (k *Kube) Plan(ctx context.Context, target string) (Plan, error) {
	ns, name, err := splitTarget(target)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Platform: "kubernetes", Connection: k.connection, Target: target}
	if !k8sName.MatchString(ns) || !k8sName.MatchString(name) {
		return p, fmt.Errorf("%q is not a valid Kubernetes namespace/name", target)
	}
	if protectedNS[ns] {
		p.Reason = fmt.Sprintf("namespace %q is a protected system namespace; PhantomOps will not modify it", ns)
		return p, nil
	}

	raw, err := k.run(ctx, "-n", ns, "get", "deployment", name, "-o", "json")
	if err != nil {
		return p, fmt.Errorf("cannot read deployment %s: %w", target, err)
	}
	var d deploymentDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return p, fmt.Errorf("decode deployment: %w", err)
	}

	hist, err := k.run(ctx, "-n", ns, "rollout", "history", "deployment/"+name)
	if err != nil {
		return p, fmt.Errorf("cannot read rollout history: %w", err)
	}
	revs := parseRevisions(string(hist))
	if len(revs) < 2 {
		p.Reason = "there is no previous revision to roll back to (this deployment has only one revision, e.g. it was just created)"
		p.Current = describeRevision(revs, 0, images(d))
		return p, nil
	}
	cur, prev := revs[0], revs[1]
	p.Current = fmt.Sprintf("revision %d (%s)", cur, strings.Join(images(d), ", "))
	prevImgs := k.revisionImages(ctx, ns, name, prev)
	p.Previous = fmt.Sprintf("revision %d (%s)", prev, orUnknown(prevImgs))
	p.Command = k.displayCommand(ns, name, prev)
	p.fingerprint = fmt.Sprintf("%d>%d", cur, prev)

	// Show what would really change; "previous" is not always "last good".
	curT, err1 := k.revisionTemplate(ctx, ns, name, cur)
	prevT, err2 := k.revisionTemplate(ctx, ns, name, prev)
	if err1 != nil || err2 != nil {
		p.Notes = append(p.Notes, "Could not compare the two revisions, so the exact changes are unknown.")
	} else if p.Changes = diffContainers(curT, prevT); len(p.Changes) == 0 {
		p.Reason = fmt.Sprintf("revisions %d and %d have identical pod templates; rolling back would not change anything", cur, prev)
		return p, nil
	}
	p.Feasible = true
	p.Notes = []string{
		"Kubernetes will create new pods from the previous revision's pod template and retire the current ones.",
		"Only the pod template is reverted; replica count and other fields are left as they are.",
	}
	if len(prevImgs) > 0 && strings.Join(prevImgs, ",") == strings.Join(images(d), ",") {
		p.Notes = append(p.Notes, "The previous revision uses the same image as the current one; the difference is in the configuration (env, resources, command...).")
	}
	return p, nil
}

// Execute re-plans, aborts if the state moved, performs the rollback and
// waits for the rollout to become healthy.
func (k *Kube) Execute(ctx context.Context, plan Plan) (Result, error) {
	if !plan.Feasible {
		return Result{}, errors.New("plan is not feasible: " + plan.Reason)
	}
	fresh, err := k.Plan(ctx, plan.Target)
	if err != nil {
		return Result{}, err
	}
	if !fresh.Feasible || fresh.fingerprint != plan.fingerprint {
		return Result{}, ErrStateChanged
	}
	ns, name, _ := splitTarget(plan.Target)
	var cur, prev int
	if _, err := fmt.Sscanf(plan.fingerprint, "%d>%d", &cur, &prev); err != nil {
		return Result{}, errors.New("internal error: bad plan fingerprint")
	}

	if _, err := k.run(ctx, "-n", ns, "rollout", "undo", "deployment/"+name, "--to-revision="+strconv.Itoa(prev)); err != nil {
		return Result{}, fmt.Errorf("rollback failed: %w", err)
	}
	res := Result{Applied: true}
	out, err := k.run(ctx, "-n", ns, "rollout", "status", "deployment/"+name, "--timeout=90s")
	if err != nil {
		res.Message = "Rollback applied, but the rollout did not become healthy within 90 s. Check the pods: " + strings.TrimSpace(err.Error())
		return res, nil
	}
	res.Healthy = true
	res.Message = fmt.Sprintf("Rolled %s back to revision %d. %s", plan.Target, prev, strings.TrimSpace(lastLine(string(out))))
	return res, nil
}

// revisionImages returns the container images of one revision (best effort).
func (k *Kube) revisionImages(ctx context.Context, ns, name string, rev int) []string {
	out, err := k.run(ctx, "-n", ns, "rollout", "history", "deployment/"+name, "--revision="+strconv.Itoa(rev))
	if err != nil {
		return nil
	}
	var imgs []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Image:") {
			imgs = append(imgs, strings.TrimSpace(strings.TrimPrefix(l, "Image:")))
		}
	}
	return imgs
}

// parseRevisions extracts revision numbers from `kubectl rollout history`,
// highest first.
func parseRevisions(out string) []int {
	var revs []int
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.Atoi(f[0]); err == nil {
			revs = append(revs, n)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(revs)))
	return revs
}

func images(d deploymentDoc) []string {
	var out []string
	for _, c := range d.Spec.Template.Spec.Containers {
		out = append(out, c.Image)
	}
	return out
}

func describeRevision(revs []int, i int, imgs []string) string {
	if i < len(revs) {
		return fmt.Sprintf("revision %d (%s)", revs[i], strings.Join(imgs, ", "))
	}
	return strings.Join(imgs, ", ")
}

func orUnknown(s []string) string {
	if len(s) == 0 {
		return "image unknown"
	}
	return strings.Join(s, ", ")
}

func lastLine(s string) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	return ls[len(ls)-1]
}

// --- pod template comparison -------------------------------------------------

type podContainer struct {
	Name    string   `json:"name"`
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Args    []string `json:"args"`
	Env     []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"env"`
	Resources struct {
		Limits   map[string]string `json:"limits"`
		Requests map[string]string `json:"requests"`
	} `json:"resources"`
}

type revisionDoc struct {
	Spec struct {
		Template struct {
			Spec struct {
				Containers []podContainer `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

// revisionTemplate fetches the containers of one revision.
func (k *Kube) revisionTemplate(ctx context.Context, ns, name string, rev int) ([]podContainer, error) {
	out, err := k.run(ctx, "-n", ns, "rollout", "history", "deployment/"+name, "--revision="+strconv.Itoa(rev), "-o", "json")
	if err != nil {
		return nil, err
	}
	var d revisionDoc
	if err := json.Unmarshal(out, &d); err != nil {
		return nil, err
	}
	return d.Spec.Template.Spec.Containers, nil
}

func short(s string) string {
	if len(s) > 70 {
		return s[:70] + "…"
	}
	return s
}

func joinCmd(c []string) string { return short(strings.Join(c, " ")) }

// diffContainers lists what changes when going from the current containers
// to the previous ones. Environment values are never printed (they may be
// secrets); only variable names are.
func diffContainers(cur, prev []podContainer) []string {
	var out []string
	prevBy := map[string]podContainer{}
	for _, c := range prev {
		prevBy[c.Name] = c
	}
	for _, c := range cur {
		p, ok := prevBy[c.Name]
		if !ok {
			out = append(out, fmt.Sprintf("container %s: removed (it does not exist in the previous revision)", c.Name))
			continue
		}
		pre := "container " + c.Name + ": "
		if c.Image != p.Image {
			out = append(out, fmt.Sprintf("%simage %s → %s", pre, short(c.Image), short(p.Image)))
		}
		if strings.Join(c.Command, "\x00") != strings.Join(p.Command, "\x00") {
			out = append(out, fmt.Sprintf("%scommand %q → %q", pre, joinCmd(c.Command), joinCmd(p.Command)))
		}
		if strings.Join(c.Args, "\x00") != strings.Join(p.Args, "\x00") {
			out = append(out, fmt.Sprintf("%sargs %q → %q", pre, joinCmd(c.Args), joinCmd(p.Args)))
		}
		curEnv, prevEnv := map[string]string{}, map[string]string{}
		for _, e := range c.Env {
			curEnv[e.Name] = e.Value
		}
		for _, e := range p.Env {
			prevEnv[e.Name] = e.Value
		}
		var names []string
		for n := range curEnv {
			names = append(names, n)
		}
		for n := range prevEnv {
			if _, ok := curEnv[n]; !ok {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			cv, inCur := curEnv[n]
			pv, inPrev := prevEnv[n]
			switch {
			case inCur && !inPrev:
				out = append(out, fmt.Sprintf("%senv %s will be removed", pre, n))
			case !inCur && inPrev:
				out = append(out, fmt.Sprintf("%senv %s will be added", pre, n))
			case cv != pv:
				out = append(out, fmt.Sprintf("%senv %s will change value", pre, n))
			}
		}
		out = append(out, diffResources(pre+"limits", c.Resources.Limits, p.Resources.Limits)...)
		out = append(out, diffResources(pre+"requests", c.Resources.Requests, p.Resources.Requests)...)
	}
	return out
}

func diffResources(label string, cur, prev map[string]string) []string {
	keys := map[string]bool{}
	for k := range cur {
		keys[k] = true
	}
	for k := range prev {
		keys[k] = true
	}
	var ks []string
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	var out []string
	for _, k := range ks {
		if cur[k] != prev[k] {
			out = append(out, fmt.Sprintf("%s.%s %s → %s", label, k, orNone(cur[k]), orNone(prev[k])))
		}
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
