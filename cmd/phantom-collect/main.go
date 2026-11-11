// Command phantom-collect runs a collector once and prints its events as JSON.
// It is a development tool for exercising collectors without the GUI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"PhantomOps/internal/collector"
	"PhantomOps/internal/collector/docker"
	"PhantomOps/internal/collector/kubernetes"
	"PhantomOps/internal/collector/loki"
	"PhantomOps/internal/collector/nomad"
	"PhantomOps/internal/collector/prometheus"
	sshc "PhantomOps/internal/collector/ssh"
)

func main() {
	src := flag.String("source", "docker", "collector to run (docker, kubernetes, prometheus, loki, nomad, ssh)")
	socket := flag.String("docker-socket", docker.DefaultSocket, "Docker socket path")
	kubeCtx := flag.String("kube-context", "", "kubectl context (default: current)")
	promURL := flag.String("prometheus-url", "http://localhost:9090", "Prometheus base URL")
	lokiURL := flag.String("loki-url", "http://localhost:3100", "Loki base URL")
	lokiQuery := flag.String("loki-query", "", "LogQL query (default: error-ish lines from all services)")
	nomadURL := flag.String("nomad-url", "http://localhost:4646", "Nomad base URL")
	nomadToken := flag.String("nomad-token", os.Getenv("NOMAD_TOKEN"), "Nomad ACL token (default: $NOMAD_TOKEN)")
	sshHost := flag.String("ssh-host", "", "SSH target host[:port]")
	sshUser := flag.String("ssh-user", os.Getenv("USER"), "SSH user")
	sshKey := flag.String("ssh-key", "", "SSH private key (default: use ssh-agent)")
	sshKnown := flag.String("ssh-known-hosts", "", "known_hosts file (default ~/.ssh/known_hosts)")
	sshInsecure := flag.Bool("ssh-insecure", false, "skip host key verification (testing only)")
	sshPorts := flag.String("ssh-ports", "", "comma-separated TCP ports expected to be listening")
	window := flag.Duration("since", time.Hour, "how far back to look")
	flag.Parse()

	var c collector.Collector
	switch *src {
	case "docker":
		c = docker.New(*socket)
	case "kubernetes":
		c = kubernetes.New(*kubeCtx)
	case "prometheus":
		c = prometheus.New(*promURL, nil)
	case "loki":
		c = loki.New(*lokiURL, *lokiQuery)
	case "nomad":
		c = nomad.New(*nomadURL, *nomadToken)
	case "ssh":
		cfg := sshc.Config{Addr: *sshHost, User: *sshUser, KeyPath: *sshKey,
			KnownHosts: *sshKnown, Insecure: *sshInsecure}
		for _, p := range strings.Split(*sshPorts, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid port %q\n", p)
				os.Exit(2)
			}
			cfg.ExpectedPorts = append(cfg.ExpectedPorts, n)
		}
		sc, err := sshc.New(cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		c = sc
	default:
		fmt.Fprintf(os.Stderr, "unknown source %q\n", *src)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	events, err := c.Collect(ctx, time.Now().Add(-*window))
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(events)
}
