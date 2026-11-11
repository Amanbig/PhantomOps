// Package docker collects container events from a Docker-compatible engine
// (Docker, or Podman via its Docker-compatible socket).
package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DefaultSocket is the standard Docker socket path.
const DefaultSocket = "/var/run/docker.sock"

// apiClient is a minimal Docker Engine API client over a unix socket.
type apiClient struct {
	http *http.Client
}

func newAPIClient(socket string) *apiClient {
	return &apiClient{http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		},
	}}
}

func (c *apiClient) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	u := "http://docker" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("docker api %s: %s: %s", path, resp.Status, bytes.TrimSpace(body))
	}
	return body, nil
}

func (c *apiClient) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	b, err := c.get(ctx, path, q)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

type containerSummary struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
}

type containerInspect struct {
	ID           string `json:"Id"`
	Name         string `json:"Name"`
	RestartCount int    `json:"RestartCount"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		OOMKilled  bool   `json:"OOMKilled"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image string `json:"Image"`
		Tty   bool   `json:"Tty"`
	} `json:"Config"`
}

type containerStats struct {
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
		Stats struct {
			InactiveFile uint64 `json:"inactive_file"`
		} `json:"stats"`
	} `json:"memory_stats"`
}

// workingSet mirrors `docker stats`: usage minus reclaimable page cache.
func (s containerStats) workingSet() uint64 {
	u, c := s.MemoryStats.Usage, s.MemoryStats.Stats.InactiveFile
	if c > u {
		return u
	}
	return u - c
}

// demuxLogs converts Docker's multiplexed log stream (8-byte frame headers)
// into plain text lines. TTY containers are not multiplexed.
func demuxLogs(raw []byte, tty bool) []string {
	var out bytes.Buffer
	if tty {
		out.Write(raw)
	} else {
		for len(raw) >= 8 {
			size := int(binary.BigEndian.Uint32(raw[4:8]))
			raw = raw[8:]
			if size > len(raw) {
				size = len(raw)
			}
			out.Write(raw[:size])
			raw = raw[size:]
		}
	}
	return splitLines(out.String())
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if line := trimCR(s[start:i]); line != "" {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if line := trimCR(s[start:]); line != "" {
		lines = append(lines, line)
	}
	return lines
}

func trimCR(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
