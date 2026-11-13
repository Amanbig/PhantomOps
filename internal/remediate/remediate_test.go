package remediate

import (
	"strings"
	"testing"
	"time"
)

func TestSplitTarget(t *testing.T) {
	ns, name, err := splitTarget("prod/api")
	if err != nil || ns != "prod" || name != "api" {
		t.Errorf("got %q %q %v", ns, name, err)
	}
	for _, bad := range []string{"", "api", "/api", "prod/", "a/b/c"} {
		if _, _, err := splitTarget(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNameValidators(t *testing.T) {
	for _, ok := range []string{"api", "my-app.v2", "a1"} {
		if !k8sName.MatchString(ok) {
			t.Errorf("k8s rejected %q", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a_b", "a b", "a;b", "$(x)", strings.Repeat("a", 254)} {
		if k8sName.MatchString(bad) {
			t.Errorf("k8s accepted %q", bad)
		}
	}
	for _, ok := range []string{"web", "Web_1", "a.b-c"} {
		if !nomadName.MatchString(ok) {
			t.Errorf("nomad rejected %q", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a b", "a/b", "a?x", "a\n", strings.Repeat("a", 129)} {
		if nomadName.MatchString(bad) {
			t.Errorf("nomad accepted %q", bad)
		}
	}
}

func TestApprovalsAreSingleUse(t *testing.T) {
	a := NewApprovals()
	tok := a.Issue(Pending{IncidentID: "inc1", Plan: Plan{Target: "default/api"}})
	if len(tok) != 32 {
		t.Errorf("token = %q", tok)
	}
	if p, err := a.Peek(tok); err != nil || p.IncidentID != "inc1" {
		t.Errorf("peek = %+v %v", p, err)
	}
	if _, err := a.Peek(tok); err != nil {
		t.Error("Peek must not consume the token")
	}
	if p, err := a.Redeem(tok); err != nil || p.Plan.Target != "default/api" {
		t.Errorf("redeem = %+v %v", p, err)
	}
	if _, err := a.Redeem(tok); err == nil {
		t.Error("a token must work only once")
	}
	if _, err := a.Peek(tok); err == nil {
		t.Error("a redeemed token must be gone")
	}
}

func TestApprovalsExpire(t *testing.T) {
	a := NewApprovals()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	fresh := a.Issue(Pending{IncidentID: "fresh"})
	now = now.Add(ApprovalTTL - time.Second)
	if _, err := a.Peek(fresh); err != nil {
		t.Errorf("token must still be valid just before the TTL: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := a.Peek(fresh); err == nil {
		t.Error("an expired token must not be previewable")
	}
	if _, err := a.Redeem(fresh); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("an expired token must not be redeemable: %v", err)
	}
	if _, err := a.Redeem("0123456789abcdef0123456789abcdef"); err == nil {
		t.Error("an unknown token must be refused")
	}
}

func TestApprovalsDropExpiredEntriesOnIssue(t *testing.T) {
	a := NewApprovals()
	now := time.Now()
	a.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		a.Issue(Pending{})
	}
	now = now.Add(ApprovalTTL + time.Minute)
	a.Issue(Pending{})
	if len(a.items) != 1 {
		t.Errorf("%d entries kept, want 1", len(a.items))
	}
}

func TestTokensAreUnique(t *testing.T) {
	a := NewApprovals()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok := a.Issue(Pending{})
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}
