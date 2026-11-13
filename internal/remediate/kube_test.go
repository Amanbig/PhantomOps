package remediate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeKube answers the kubectl calls Kube makes and records every call.
type fakeKube struct {
	calls     []string
	deploy    string
	history   string
	templates map[int]string // revision -> JSON for `-o json`
	images    map[int]string // revision -> plain `--revision=N` output
	undoErr   error
	statusErr error
	getErr    error
}

func (f *fakeKube) run(_ context.Context, args ...string) ([]byte, error) {
	j := strings.Join(args, " ")
	f.calls = append(f.calls, j)
	rev := func() int {
		var n int
		for _, a := range args {
			if strings.HasPrefix(a, "--revision=") {
				fmt.Sscanf(strings.TrimPrefix(a, "--revision="), "%d", &n)
			}
		}
		return n
	}
	switch {
	case strings.Contains(j, "get deployment"):
		if f.getErr != nil {
			return nil, f.getErr
		}
		return []byte(f.deploy), nil
	case strings.Contains(j, "rollout history") && strings.Contains(j, "-o json"):
		if t, ok := f.templates[rev()]; ok {
			return []byte(t), nil
		}
		return nil, errors.New("no such revision")
	case strings.Contains(j, "rollout history") && strings.Contains(j, "--revision="):
		return []byte(f.images[rev()]), nil
	case strings.Contains(j, "rollout history"):
		return []byte(f.history), nil
	case strings.Contains(j, "rollout undo"):
		return nil, f.undoErr
	case strings.Contains(j, "rollout status"):
		return []byte("Waiting for rollout to finish\ndeployment \"api\" successfully rolled out\n"), f.statusErr
	}
	return nil, fmt.Errorf("unexpected kubectl call: %s", j)
}

func (f *fakeKube) called(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func template(image, envValue string) string {
	return fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":"api","image":%q,
		"env":[{"name":"DB_PASSWORD","value":%q}],"resources":{"limits":{"memory":"256Mi"}}}]}}}}`, image, envValue)
}

func newFake() *fakeKube {
	return &fakeKube{
		deploy:  `{"spec":{"template":{"spec":{"containers":[{"image":"api:v2"}]}}}}`,
		history: "deployment.apps/api\nREVISION  CHANGE-CAUSE\n1         <none>\n2         <none>\n",
		templates: map[int]string{
			2: template("api:v2", "new-secret-value"),
			1: template("api:v1", "old-secret-value"),
		},
		images: map[int]string{1: "Pod Template:\n  Image:\tapi:v1\n", 2: "Pod Template:\n  Image:\tapi:v2\n"},
	}
}

func TestKubePlanFeasibleDescribesChangesWithoutLeakingEnvValues(t *testing.T) {
	f := newFake()
	p, err := NewKubeWithRunner("k3s", f.run).Plan(context.Background(), "default/api")
	if err != nil || !p.Feasible {
		t.Fatalf("plan = %+v err=%v", p, err)
	}
	if p.Platform != "kubernetes" || p.Connection != "k3s" || p.Target != "default/api" {
		t.Errorf("identity = %+v", p)
	}
	if p.Command != "kubectl -n default rollout undo deployment/api --to-revision=1" {
		t.Errorf("command = %q", p.Command)
	}
	if !strings.Contains(p.Current, "revision 2") || !strings.Contains(p.Previous, "revision 1 (api:v1)") {
		t.Errorf("current/previous = %q / %q", p.Current, p.Previous)
	}
	all := strings.Join(p.Changes, "\n")
	if !strings.Contains(all, "image api:v2 → api:v1") || !strings.Contains(all, "env DB_PASSWORD will change value") {
		t.Errorf("changes = %v", p.Changes)
	}
	for _, secret := range []string{"new-secret-value", "old-secret-value"} {
		if strings.Contains(all, secret) || strings.Contains(strings.Join(p.Notes, "\n"), secret) {
			t.Errorf("environment value %q leaked into the plan", secret)
		}
	}
	for _, c := range f.calls {
		if strings.Contains(c, "undo") {
			t.Fatal("Plan must never change anything")
		}
	}
}

func TestKubePlanRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*fakeKube)
		target string
		want   string
		calls  bool // whether kubectl may have been called
	}{
		{"protected namespace", func(*fakeKube) {}, "kube-system/coredns", "protected system namespace", false},
		{"kube-public", func(*fakeKube) {}, "kube-public/x", "protected system namespace", false},
		{"only one revision", func(f *fakeKube) {
			f.history = "REVISION  CHANGE-CAUSE\n1         <none>\n"
		}, "default/api", "no previous revision", true},
		{"identical templates", func(f *fakeKube) {
			f.templates[1] = f.templates[2]
		}, "default/api", "identical pod templates", true},
	}
	for _, c := range cases {
		f := newFake()
		c.mutate(f)
		p, err := NewKubeWithRunner("k3s", f.run).Plan(context.Background(), c.target)
		if err != nil || p.Feasible || !strings.Contains(p.Reason, c.want) {
			t.Errorf("%s: feasible=%v reason=%q err=%v", c.name, p.Feasible, p.Reason, err)
		}
		if !c.calls && len(f.calls) != 0 {
			t.Errorf("%s: kubectl must not be called, got %v", c.name, f.calls)
		}
	}
}

func TestKubePlanRejectsUnsafeTargetsWithoutRunningAnything(t *testing.T) {
	for _, target := range []string{
		"", "api", "/api", "default/", "a/b/c", "default/api;reboot", "default/$(id)", "default/api name",
		"Default/api", "default/API", "-n/api", "default/api\nrollout", "default/-api",
	} {
		f := newFake()
		_, err := NewKubeWithRunner("k3s", f.run).Plan(context.Background(), target)
		if err == nil {
			t.Errorf("target %q was accepted", target)
		}
		if len(f.calls) != 0 {
			t.Errorf("target %q reached kubectl: %v", target, f.calls)
		}
	}
}

func TestKubePlanReadErrors(t *testing.T) {
	f := newFake()
	f.getErr = errors.New("forbidden")
	if _, err := NewKubeWithRunner("k3s", f.run).Plan(context.Background(), "default/api"); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("err = %v", err)
	}
}

func TestKubePlanWarnsWhenRevisionsCannotBeCompared(t *testing.T) {
	f := newFake()
	delete(f.templates, 1) // the diff is impossible
	p, err := NewKubeWithRunner("k3s", f.run).Plan(context.Background(), "default/api")
	if err != nil || !p.Feasible {
		t.Fatalf("plan = %+v err=%v", p, err)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "Could not compare") {
		t.Errorf("the user must be told the exact changes are unknown; notes = %v", p.Notes)
	}
}

func TestKubeExecuteRollsBackToThePlannedRevision(t *testing.T) {
	f := newFake()
	k := NewKubeWithRunner("k3s", f.run)
	plan, _ := k.Plan(context.Background(), "default/api")
	res, err := k.Execute(context.Background(), plan)
	if err != nil || !res.Applied || !res.Healthy || !strings.Contains(res.Message, "revision 1") {
		t.Fatalf("res = %+v err=%v", res, err)
	}
	if !f.called("-n default rollout undo deployment/api --to-revision=1") {
		t.Errorf("undo not issued with the planned revision: %v", f.calls)
	}
	if strings.Contains(res.Message, "Waiting") {
		t.Errorf("message should use only the last status line: %q", res.Message)
	}
}

func TestKubeExecuteRefusesWithoutAFeasiblePlan(t *testing.T) {
	f := newFake()
	_, err := NewKubeWithRunner("k3s", f.run).Execute(context.Background(), Plan{Target: "default/api", Reason: "nope"})
	if err == nil || len(f.calls) != 0 {
		t.Errorf("err=%v calls=%v", err, f.calls)
	}
}

func TestKubeExecuteAbortsWhenStateMovedAfterThePlan(t *testing.T) {
	f := newFake()
	k := NewKubeWithRunner("k3s", f.run)
	plan, _ := k.Plan(context.Background(), "default/api")
	// Someone deployed again after the user saw the plan.
	f.history = "REVISION  CHANGE-CAUSE\n1  <none>\n2  <none>\n3  <none>\n"
	f.templates[3] = template("api:v3", "x")
	_, err := k.Execute(context.Background(), plan)
	if !errors.Is(err, ErrStateChanged) {
		t.Fatalf("err = %v, want ErrStateChanged", err)
	}
	if f.called("rollout undo") {
		t.Error("a stale plan must never be executed")
	}
}

func TestKubeExecuteFailureAndUnhealthyRollout(t *testing.T) {
	f := newFake()
	k := NewKubeWithRunner("k3s", f.run)
	plan, _ := k.Plan(context.Background(), "default/api")

	f.undoErr = errors.New("forbidden")
	if res, err := k.Execute(context.Background(), plan); err == nil || res.Applied {
		t.Errorf("failed undo: res=%+v err=%v", res, err)
	}

	f.undoErr, f.statusErr = nil, errors.New("timed out waiting for the condition")
	res, err := k.Execute(context.Background(), plan)
	if err != nil || !res.Applied || res.Healthy || !strings.Contains(res.Message, "did not become healthy") {
		t.Errorf("unhealthy rollout: res=%+v err=%v", res, err)
	}
}

func TestParseRevisionsAndDisplayCommand(t *testing.T) {
	got := parseRevisions("deployment.apps/api\nREVISION  CHANGE-CAUSE\n3  <none>\n10  rollout\n2  <none>\nnot a revision\n")
	if fmt.Sprint(got) != "[10 3 2]" {
		t.Errorf("revisions = %v", got)
	}
	k := &Kube{kubeContext: "prod"}
	if c := k.displayCommand("web", "api", 4); c != "kubectl --context prod -n web rollout undo deployment/api --to-revision=4" {
		t.Errorf("command = %q", c)
	}
}

func TestDiffContainers(t *testing.T) {
	cur := []podContainer{{Name: "api", Image: "api:v2", Args: []string{"--fast"}}, {Name: "sidecar", Image: "s:1"}}
	prev := []podContainer{{Name: "api", Image: "api:v1", Args: []string{"--safe"}}}
	got := strings.Join(diffContainers(cur, prev), "\n")
	for _, want := range []string{"image api:v2 → api:v1", `args "--fast" → "--safe"`, "container sidecar: removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if d := diffContainers(cur[:1], cur[:1]); len(d) != 0 {
		t.Errorf("identical containers differ: %v", d)
	}
}
