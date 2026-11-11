// Package remediate performs reviewed, reversible changes to infrastructure
// (currently: rolling a Kubernetes Deployment or a Nomad job back to its
// previous revision).
//
// Safety model:
//   - Plan never changes anything; it inspects the target, decides whether the
//     action is feasible and describes exactly what Execute would do.
//   - Execute re-plans immediately before acting and aborts if the state moved
//     since the plan was shown to the user.
//   - Rollbacks are refused when there is no previous revision (e.g. a
//     first-ever deployment) and for protected system namespaces.
package remediate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Plan describes a rollback that has been inspected but not performed.
type Plan struct {
	Platform   string   `json:"platform"`   // "kubernetes" or "nomad"
	Connection string   `json:"connection"` // connection name
	Target     string   `json:"target"`     // "namespace/name"
	Feasible   bool     `json:"feasible"`
	Reason     string   `json:"reason"`   // why not feasible
	Current    string   `json:"current"`  // human-readable current revision
	Previous   string   `json:"previous"` // human-readable revision it would return to
	Command    string   `json:"command"`  // the exact command / API call
	Notes      []string `json:"notes"`
	// Changes lists what the rollback would actually change in the workload.
	Changes []string `json:"changes"`

	// fingerprint pins the revisions seen at plan time.
	fingerprint string
}

// Result is the outcome of Execute.
type Result struct {
	Applied bool   `json:"applied"` // the rollback request was accepted
	Healthy bool   `json:"healthy"` // the workload was observed healthy afterwards
	Message string `json:"message"`
}

// Executor can plan and perform a rollback on one platform.
type Executor interface {
	Plan(ctx context.Context, target string) (Plan, error)
	Execute(ctx context.Context, plan Plan) (Result, error)
}

var (
	k8sName     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	nomadName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	protectedNS = map[string]bool{"kube-system": true, "kube-public": true, "kube-node-lease": true}
)

// splitTarget splits "namespace/name".
func splitTarget(target string) (ns, name string, err error) {
	parts := strings.Split(target, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("target %q must look like namespace/name", target)
	}
	return parts[0], parts[1], nil
}

// ErrStateChanged is returned by Execute when the target changed after the
// plan was produced.
var ErrStateChanged = errors.New("the target changed since the plan was shown; review a fresh plan")

// --- approvals --------------------------------------------------------------

// ApprovalTTL is how long a shown plan can be approved.
const ApprovalTTL = 2 * time.Minute

// Pending is a plan awaiting the user's approval.
type Pending struct {
	Plan       Plan
	IncidentID string
	Executor   Executor
	expires    time.Time
}

// Approvals hands out single-use, expiring tokens that bind an approval to a
// plan the user has actually been shown.
type Approvals struct {
	mu    sync.Mutex
	items map[string]Pending
	now   func() time.Time
}

// NewApprovals creates an empty approval store.
func NewApprovals() *Approvals {
	return &Approvals{items: map[string]Pending{}, now: time.Now}
}

// Issue stores the plan and returns its approval token.
func (a *Approvals) Issue(p Pending) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	a.mu.Lock()
	defer a.mu.Unlock()
	for k, v := range a.items { // drop expired entries
		if a.now().After(v.expires) {
			delete(a.items, k)
		}
	}
	p.expires = a.now().Add(ApprovalTTL)
	a.items[tok] = p
	return tok
}

// Peek returns the pending plan for a token without consuming it.
func (a *Approvals) Peek(tok string) (Pending, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.items[tok]
	if !ok || a.now().After(p.expires) {
		return Pending{}, errors.New("approval not found or expired; request a new preview")
	}
	return p, nil
}

// Redeem consumes a token. A token works once and only before it expires.
func (a *Approvals) Redeem(tok string) (Pending, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.items[tok]
	if !ok {
		return Pending{}, errors.New("approval not found or already used; request a new preview")
	}
	delete(a.items, tok)
	if a.now().After(p.expires) {
		return Pending{}, errors.New("approval expired; request a new preview")
	}
	return p, nil
}
