package remediate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeNomad serves the few Nomad endpoints the executor uses.
type fakeNomad struct {
	mu       sync.Mutex
	jobType  string
	version  uint64
	versions string // JSON array for /versions
	reverts  []map[string]any
	tokens   []string
	status   int // overrides the answer to every request when non-zero
}

func (f *fakeNomad) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tokens = append(f.tokens, r.Header.Get("X-Nomad-Token"))
		if f.status != 0 {
			http.Error(w, "denied", f.status)
			return
		}
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/revert"):
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			_ = json.Unmarshal(b, &body)
			f.reverts = append(f.reverts, body)
			f.version++ // the revert registers a new version
			fmt.Fprint(w, `{}`)
		case strings.HasSuffix(r.URL.Path, "/versions"):
			fmt.Fprintf(w, `{"Versions":%s}`, f.versions)
		case strings.HasSuffix(r.URL.Path, "/allocations"):
			fmt.Fprintf(w, `[{"JobVersion":%d,"ClientStatus":"running","DesiredStatus":"run"}]`, f.version)
		default:
			fmt.Fprintf(w, `{"ID":"web","Type":%q,"Version":%d,"Status":"running"}`, f.jobType, f.version)
		}
	})
}

func version(v int, stable bool, image, env string) string {
	return fmt.Sprintf(`{"Version":%d,"Stable":%v,"TaskGroups":[{"Name":"g","Tasks":[{"Name":"t",
		"Config":{"image":%q},"Env":{"API_KEY":%q},"Resources":{"CPU":100,"MemoryMB":128}}]}]}`, v, stable, image, env)
}

func newNomad(t *testing.T) (*fakeNomad, *Nomad) {
	t.Helper()
	f := &fakeNomad{jobType: "service", version: 3, versions: "[" + strings.Join([]string{
		version(3, false, "web:v3", "key-three"),
		version(2, false, "web:v2", "key-two"), // never became stable: must be skipped
		version(1, true, "web:v1", "key-one"),
		version(0, true, "web:v0", "key-zero"),
	}, ",") + "]"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return f, NewNomad("nomad-dev", srv.URL+"/", "s3cret-token")
}

func TestNomadPlanPicksTheLatestStableEarlierVersion(t *testing.T) {
	f, n := newNomad(t)
	p, err := n.Plan(context.Background(), "default/web")
	if err != nil || !p.Feasible {
		t.Fatalf("plan = %+v err=%v", p, err)
	}
	if !strings.Contains(p.Previous, "version 1 (web:v1)") || !strings.Contains(p.Current, "version 3 (web:v3)") {
		t.Errorf("current/previous = %q / %q", p.Current, p.Previous)
	}
	if !strings.Contains(p.Command, `"JobVersion": 1`) || !strings.Contains(p.Command, `"EnforcePriorVersion": 3`) {
		t.Errorf("command = %q", p.Command)
	}
	all := strings.Join(p.Changes, "\n")
	if !strings.Contains(all, "image web:v3 → web:v1") || !strings.Contains(all, "env API_KEY will change value") {
		t.Errorf("changes = %v", p.Changes)
	}
	for _, secret := range []string{"key-three", "key-one"} {
		if strings.Contains(all, secret) {
			t.Errorf("environment value %q leaked into the plan", secret)
		}
	}
	for _, tok := range f.tokens {
		if tok != "s3cret-token" {
			t.Errorf("token header = %q", tok)
		}
	}
	if len(f.reverts) != 0 {
		t.Error("Plan must never change anything")
	}
}

func TestNomadPlanRefusals(t *testing.T) {
	f, n := newNomad(t)
	f.jobType = "batch"
	if p, err := n.Plan(context.Background(), "default/web"); err != nil || p.Feasible || !strings.Contains(p.Reason, "only service jobs") {
		t.Errorf("batch job: %+v err=%v", p, err)
	}

	f.jobType = "service"
	f.versions = "[" + version(3, false, "web:v3", "a") + "," + version(2, false, "web:v2", "b") + "]"
	if p, _ := n.Plan(context.Background(), "default/web"); p.Feasible || !strings.Contains(p.Reason, "no earlier stable version") {
		t.Errorf("no stable version: %+v", p)
	}

	f.versions = "[" + version(3, false, "web:v3", "same") + "," + version(1, true, "web:v3", "same") + "]"
	if p, _ := n.Plan(context.Background(), "default/web"); p.Feasible || !strings.Contains(p.Reason, "identical task definitions") {
		t.Errorf("identical versions: %+v", p)
	}
}

func TestNomadPlanWarnsWhenCurrentVersionIsUnreadable(t *testing.T) {
	f, n := newNomad(t)
	f.versions = "[" + version(1, true, "web:v1", "k") + "]" // the running version 3 is missing from the list
	p, err := n.Plan(context.Background(), "default/web")
	if err != nil || !p.Feasible {
		t.Fatalf("plan = %+v err=%v", p, err)
	}
	if !strings.Contains(strings.Join(p.Notes, "\n"), "Could not read the current version") {
		t.Errorf("the user must be told the exact changes are unknown; notes = %v", p.Notes)
	}
}

func TestNomadPlanRejectsUnsafeTargetsWithoutRequests(t *testing.T) {
	f, n := newNomad(t)
	for _, target := range []string{"", "web", "default/", "default/../etc", "a/b/c", "default/web?x=1", "default/we b", "default/-web", "default/web\n"} {
		if _, err := n.Plan(context.Background(), target); err == nil {
			t.Errorf("target %q was accepted", target)
		}
	}
	if len(f.tokens) != 0 {
		t.Errorf("unsafe targets reached the server %d time(s)", len(f.tokens))
	}
}

func TestNomadPlanErrorsDoNotLeakTheToken(t *testing.T) {
	f, n := newNomad(t)
	f.status = http.StatusForbidden
	_, err := n.Plan(context.Background(), "default/web")
	if err == nil || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "s3cret-token") {
		t.Errorf("err = %v", err)
	}
}

func TestNomadExecuteRefusesWithoutFeasiblePlan(t *testing.T) {
	f, n := newNomad(t)
	if _, err := n.Execute(context.Background(), Plan{Target: "default/web", Reason: "no"}); err == nil || len(f.tokens) != 0 {
		t.Errorf("err=%v requests=%d", err, len(f.tokens))
	}
}

func TestNomadExecuteAbortsWhenJobChangedAfterThePlan(t *testing.T) {
	f, n := newNomad(t)
	plan, _ := n.Plan(context.Background(), "default/web")
	f.mu.Lock()
	f.version = 4 // someone deployed after the user saw the plan
	f.versions = "[" + version(4, false, "web:v4", "x") + "," + version(1, true, "web:v1", "k") + "]"
	f.mu.Unlock()
	if _, err := n.Execute(context.Background(), plan); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("err = %v, want ErrStateChanged", err)
	}
	if len(f.reverts) != 0 {
		t.Error("a stale plan must never be executed")
	}
}

func TestNomadExecuteRevertsWithPriorVersionGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the allocation poll interval")
	}
	f, n := newNomad(t)
	plan, _ := n.Plan(context.Background(), "default/web")
	res, err := n.Execute(context.Background(), plan)
	if err != nil || !res.Applied || !res.Healthy {
		t.Fatalf("res = %+v err=%v", res, err)
	}
	if len(f.reverts) != 1 {
		t.Fatalf("reverts = %v", f.reverts)
	}
	body := f.reverts[0]
	if body["JobID"] != "web" || body["JobVersion"] != float64(1) || body["EnforcePriorVersion"] != float64(3) {
		t.Errorf("revert body = %v", body)
	}
}

func TestNomadExecuteFailureIsReportedAsNotApplied(t *testing.T) {
	f, n := newNomad(t)
	plan, _ := n.Plan(context.Background(), "default/web")
	f.mu.Lock()
	f.status = http.StatusInternalServerError
	f.mu.Unlock()
	res, err := n.Execute(context.Background(), plan)
	if err == nil || res.Applied {
		t.Errorf("res=%+v err=%v", res, err)
	}
}

func TestDiffNomad(t *testing.T) {
	mk := func(img, mem string, memMB int) nomadVersion {
		var v nomadVersion
		_ = json.Unmarshal([]byte(version(1, true, img, mem)), &v)
		v.TaskGroups[0].Tasks[0].Resources.MemoryMB = memMB
		return v
	}
	got := strings.Join(diffNomad(mk("a:1", "x", 256), mk("a:0", "x", 128)), "\n")
	if !strings.Contains(got, "image a:1 → a:0") || !strings.Contains(got, "memory 256 MB → 128 MB") || strings.Contains(got, "env") {
		t.Errorf("diff = %s", got)
	}
	if d := diffNomad(mk("a:1", "x", 256), mk("a:1", "x", 256)); len(d) != 0 {
		t.Errorf("identical versions differ: %v", d)
	}
}
