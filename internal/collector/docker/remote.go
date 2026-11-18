package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/collector/ssh"
)

// commandRunner is implemented by the verified SSH command runner. Docker
// commands below are built from fixed subcommands and shell-quoted IDs.
type commandRunner interface {
	Run(ctx context.Context, command string) (stdout, stderr string, code int, err error)
}

type remoteAPI struct {
	runner commandRunner
	socket string
}

func newRemoteAPIClient(r commandRunner, socket string) *apiClient {
	return &apiClient{remote: (&remoteAPI{runner: r, socket: socket}).get}
}

// NewRemote creates a read-only Docker collector that runs the Docker CLI on
// the host reached through the supplied, host-key-verified SSH config.
func NewRemote(sshCfg ssh.Config, socket string) (*Collector, error) {
	r, err := ssh.Shared(sshCfg)
	if err != nil {
		return nil, err
	}
	return &Collector{api: newRemoteAPIClient(r, socket), now: func() time.Time { return time.Now().UTC() }}, nil
}

var safeContainerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func (r *remoteAPI) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	args := []string{"docker"}
	if r.socket != "" {
		args = append(args, "--host", "unix://"+r.socket)
	}
	var id string
	switch {
	case path == "/info":
		args = append(args, "info", "--format", "{{json .}}")
	case path == "/containers/json":
		args = append(args, "ps", "--all", "--no-trunc", "--format", "{{json .}}")
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		id = strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		if !safeContainerID.MatchString(id) {
			return nil, fmt.Errorf("unsafe remote container id")
		}
		args = append(args, "inspect", "--format", "{{json .}}", id)
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/stats"):
		id = strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/stats")
		if !safeContainerID.MatchString(id) {
			return nil, fmt.Errorf("unsafe remote container id")
		}
		args = append(args, "stats", "--no-stream", "--format", "{{json .}}", id)
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/logs"):
		id = strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/logs")
		if !safeContainerID.MatchString(id) {
			return nil, fmt.Errorf("unsafe remote container id")
		}
		args = append(args, "logs")
		if since := q.Get("since"); since != "" {
			args = append(args, "--since", since)
		}
		if tail := q.Get("tail"); tail != "" {
			args = append(args, "--tail", tail)
		}
		args = append(args, id)
	default:
		return nil, fmt.Errorf("unsupported remote Docker API path %q", path)
	}
	out, errOut, code, err := r.runner.Run(ctx, joinShellArgs(args))
	if err != nil {
		return nil, fmt.Errorf("remote Docker command failed: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("remote Docker command exited %d: %s", code, explainRemoteDocker(code, errOut, out))
	}
	if path == "/containers/json" {
		return normalizeRemoteList(out)
	}
	if strings.HasSuffix(path, "/stats") {
		return normalizeRemoteStats(out)
	}
	if strings.HasSuffix(path, "/logs") {
		// `docker logs` replays the container's stdout on its own stdout and the
		// container's stderr on its own stderr. Most programs log errors to
		// stderr, so keep both (the lines are only pattern-matched, so the
		// order between the two streams does not matter).
		if out != "" && errOut != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		return []byte(out + errOut), nil
	}
	return []byte(out), nil
}

func joinShellArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

// explainRemoteDocker turns the common remote failures into advice.
func explainRemoteDocker(code int, stderr, stdout string) string {
	msg := shortRemote(stderr)
	if msg == "" {
		msg = shortRemote(stdout)
	}
	l := strings.ToLower(msg)
	switch {
	case code == 127 || strings.Contains(l, "docker: command not found") || strings.Contains(l, "docker: not found"):
		return "the docker command was not found on the remote host (is Docker installed, and on the SSH user's PATH?)"
	case strings.Contains(l, "permission denied") && strings.Contains(l, "docker.sock"):
		return "the SSH user may not use Docker on the remote host. Add it to the docker group (sudo usermod -aG docker <user>), then log in again. PhantomOps does not use sudo. Details: " + msg
	case strings.Contains(l, "cannot connect to the docker daemon"):
		return "Docker is not running on the remote host, or the remote socket path is wrong. Details: " + msg
	}
	return msg
}

func shortRemote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}

type remoteList struct {
	ID     string `json:"ID"`
	Id     string `json:"Id"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Labels string `json:"Labels"`
}

func normalizeRemoteList(raw string) ([]byte, error) {
	var out = make([]map[string]any, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item remoteList
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("decode docker ps output: %w", err)
		}
		id := item.ID
		if id == "" {
			id = item.Id
		}
		names := []string{}
		for _, name := range strings.Split(item.Names, ",") {
			if name = strings.TrimSpace(name); name != "" {
				names = append(names, "/"+strings.TrimPrefix(name, "/"))
			}
		}
		labels := map[string]string{}
		for _, pair := range strings.Split(item.Labels, ",") {
			key, value, ok := strings.Cut(pair, "=")
			if ok {
				labels[key] = value
			}
		}
		out = append(out, map[string]any{"Id": id, "Names": names, "Image": item.Image,
			"State": item.State, "Status": item.Status, "Labels": labels, "Ports": []any{}})
	}
	return json.Marshal(out)
}

type remoteStats struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
}

func normalizeRemoteStats(raw string) ([]byte, error) {
	var item remoteStats
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &item); err != nil {
		return nil, fmt.Errorf("decode docker stats output: %w", err)
	}
	cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(item.CPUPerc), "%"), 64)
	usage, limit := parseMemoryUsage(item.MemUsage)
	cpuUnits := uint64(cpu * 100)
	return json.Marshal(map[string]any{
		"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": cpuUnits}, "system_cpu_usage": uint64(10000), "online_cpus": 1},
		"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": uint64(0)}, "system_cpu_usage": uint64(0)},
		"memory_stats": map[string]any{"usage": usage, "limit": limit, "stats": map[string]any{"inactive_file": 0}},
	})
}

func parseMemoryUsage(s string) (uint64, uint64) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	return parseDockerBytes(parts[0]), parseDockerBytes(parts[1])
}

func parseDockerBytes(s string) uint64 {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 {
		return 0
	}
	valueText, unit := fields[0], ""
	for i, r := range valueText {
		if (r < '0' || r > '9') && r != '.' {
			unit = valueText[i:]
			valueText = valueText[:i]
			break
		}
	}
	if unit == "" && len(fields) > 1 {
		unit = fields[1]
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return 0
	}
	unit = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(unit)), "b")
	mult := float64(1)
	switch unit {
	case "k", "ki":
		mult = 1 << 10
	case "m", "mi":
		mult = 1 << 20
	case "g", "gi":
		mult = 1 << 30
	case "t", "ti":
		mult = 1 << 40
	}
	return uint64(value * mult)
}
