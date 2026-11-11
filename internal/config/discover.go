package config

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"PhantomOps/internal/collector/docker"
)

// Discover looks for services on this machine and returns suggested
// connections (all enabled, none saved). Probes are short and read-only.
func Discover(ctx context.Context) []Connection {
	var (
		mu  sync.Mutex
		out []Connection
		wg  sync.WaitGroup
	)
	add := func(c Connection) {
		c.ID, c.Enabled = NewID(), true
		mu.Lock()
		out = append(out, c)
		mu.Unlock()
	}

	wg.Add(3)
	go func() {
		defer wg.Done()
		if conn, err := net.DialTimeout("unix", docker.DefaultSocket, time.Second); err == nil {
			conn.Close()
			add(Connection{Type: TypeDocker, Name: "Local Docker", Socket: docker.DefaultSocket})
		}
	}()
	go func() {
		defer wg.Done()
		if !onPath("kubectl") {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var buf bytes.Buffer
		cmd := exec.CommandContext(cctx, "kubectl", "config", "get-contexts", "-o", "name")
		cmd.Stdout = &buf
		if cmd.Run() != nil {
			return
		}
		for _, name := range strings.Fields(buf.String()) {
			add(Connection{Type: TypeKubernetes, Name: "Kubernetes: " + name, Context: name})
		}
	}()
	go func() {
		defer wg.Done()
		probes := []struct {
			typ, name, base, path string
		}{
			{TypePrometheus, "Prometheus (localhost)", "http://localhost:9090", "/-/ready"},
			{TypeLoki, "Loki (localhost)", "http://localhost:3100", "/ready"},
			{TypeNomad, "Nomad (localhost)", "http://localhost:4646", "/v1/agent/health"},
		}
		var pw sync.WaitGroup
		for _, p := range probes {
			pw.Add(1)
			go func() {
				defer pw.Done()
				if probeOK(ctx, p.base+p.path) {
					add(Connection{Type: p.typ, Name: p.name, URL: p.base})
				}
			}()
		}
		pw.Wait()
	}()
	wg.Wait()
	return out
}

func probeOK(ctx context.Context, u string) bool {
	cctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
