package docker

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeRemote struct {
	commands []string
}

func (f *fakeRemote) Run(_ context.Context, command string) (string, string, int, error) {
	f.commands = append(f.commands, command)
	switch {
	case strings.Contains(command, "'info'"):
		return `{"ServerVersion":"27.3.1","OperatingSystem":"Ubuntu","KernelVersion":"6.8","NCPU":4,"MemTotal":8589934592,"Images":7}`, "", 0, nil
	case strings.Contains(command, "'ps'"):
		return `{"ID":"abc123","Names":"api,api-old","Image":"demo/api:v2","State":"running","Status":"Up 2 hours","Labels":"com.docker.compose.project=demo"}
`, "", 0, nil
	case strings.Contains(command, "'inspect'"):
		return `{"Id":"abc123","Name":"/api","RestartCount":2,"State":{"Status":"running","Running":true,"OOMKilled":false,"ExitCode":0,"StartedAt":"2026-03-01T10:00:00Z","FinishedAt":"0001-01-01T00:00:00Z","Health":{"Status":"healthy"}},"Config":{"Image":"demo/api:v2","Tty":false}}`, "", 0, nil
	case strings.Contains(command, "'stats'"):
		return `{"CPUPerc":"12.5%","MemUsage":"128MiB / 512MiB"}`, "", 0, nil
	case strings.Contains(command, "'logs'"):
		return "ERROR database refused\n", "", 0, nil
	default:
		return "unexpected", "", 1, nil
	}
}

func TestRemoteAPIRendersReadOnlyDockerCLICommands(t *testing.T) {
	f := &fakeRemote{}
	api := newRemoteAPIClient(f, "/var/run/docker.sock")
	var info struct {
		ServerVersion string `json:"ServerVersion"`
		NCPU          int    `json:"NCPU"`
	}
	if err := api.getJSON(context.Background(), "/info", nil, &info); err != nil || info.ServerVersion != "27.3.1" || info.NCPU != 4 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	var list []listEntry
	if err := api.getJSON(context.Background(), "/containers/json", nil, &list); err != nil || len(list) != 1 || list[0].ID != "abc123" || list[0].Labels["com.docker.compose.project"] != "demo" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	var stats fullStats
	if err := api.getJSON(context.Background(), "/containers/abc123/stats", nil, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.MemoryStats.Usage != 128*1024*1024 || stats.MemoryStats.Limit != 512*1024*1024 || stats.CPUStats.CPUUsage.TotalUsage != 1250 {
		t.Errorf("stats memory=%+v cpu=%+v", stats.MemoryStats, stats.CPUStats)
	}
	if len(f.commands) == 0 {
		t.Fatal("no remote commands were run")
	}
	for _, cmd := range f.commands {
		if strings.Contains(cmd, "rm ") || strings.Contains(cmd, " stop ") || strings.Contains(cmd, " exec ") || strings.Contains(cmd, " run ") {
			t.Errorf("mutating command generated: %q", cmd)
		}
		if !strings.Contains(cmd, "'docker'") || !strings.Contains(cmd, "'--host'") {
			t.Errorf("command is not explicit: %q", cmd)
		}
	}
}

func TestRemoteLogsArePlainTextAndIDsAreValidated(t *testing.T) {
	f := &fakeRemote{}
	api := newRemoteAPIClient(f, "")
	c := &Collector{api: api, now: func() time.Time { return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) }}
	events := c.logEvents(context.Background(), "abc123", "api", false, time.Time{})
	if len(events) != 1 || !strings.Contains(events[0].Detail, "database refused") {
		t.Fatalf("events=%+v", events)
	}
	if _, err := api.get(context.Background(), "/containers/abc;rm -rf/json", nil); err == nil {
		t.Error("unsafe container id accepted")
	}
}

func TestRemoteListEmptyIsAnArray(t *testing.T) {
	b, err := normalizeRemoteList("\n")
	if err != nil || string(b) != "[]" {
		t.Errorf("empty list=%s err=%v", b, err)
	}
}

func TestDockerByteParsing(t *testing.T) {
	cases := map[string]uint64{"1KiB": 1024, "2MiB": 2 * 1024 * 1024, "1GiB": 1 << 30, "12 B": 12}
	for in, want := range cases {
		if got := parseDockerBytes(in); got != want {
			t.Errorf("parseDockerBytes(%q)=%d want %d", in, got, want)
		}
	}
}

// stderrRemote answers `docker logs` the way the real CLI does: what the
// container wrote to stdout goes to the CLI's stdout, and what it wrote to
// stderr (where most programs log errors) goes to the CLI's stderr.
type stderrRemote struct{ stdout, stderr string }

func (s stderrRemote) Run(_ context.Context, command string) (string, string, int, error) {
	if strings.Contains(command, "'logs'") {
		return s.stdout, s.stderr, 0, nil
	}
	return "", "", 1, nil
}

func TestRemoteLogsIncludeWhatTheContainerWroteToStderr(t *testing.T) {
	cases := []struct {
		name           string
		stdout, stderr string
		want           []string
	}{
		{"errors only on stderr (the common case)", "starting up\nlistening on :8080\n", "level=error msg=\"db unreachable\"\nFATAL: giving up\n", []string{"db unreachable", "giving up"}},
		{"errors on both streams", "ERROR from stdout\n", "ERROR from stderr\n", []string{"from stdout", "from stderr"}},
		{"stdout without trailing newline", "ERROR no newline", "ERROR second\n", []string{"no newline", "second"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Collector{api: newRemoteAPIClient(stderrRemote{tc.stdout, tc.stderr}, ""), now: func() time.Time { return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) }}
			events := c.logEvents(context.Background(), "abc123", "api", false, time.Time{})
			if len(events) != 1 {
				t.Fatalf("want one error-log event, got %+v", events)
			}
			// Two separate error lines in every case: a missing newline between
			// the two streams would glue them into one.
			if !strings.Contains(events[0].Title, "2 error log line(s)") {
				t.Errorf("want 2 separate error lines, title is %q", events[0].Title)
			}
			for _, w := range tc.want {
				if !strings.Contains(events[0].Detail, w) {
					t.Errorf("missing %q in %q", w, events[0].Detail)
				}
			}
		})
	}
}
