// Command phantom-collect runs a collector once and prints its events as JSON.
// It is a development tool for exercising collectors without the GUI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"PhantomOps/internal/collector"
	"PhantomOps/internal/collector/docker"
	"PhantomOps/internal/collector/kubernetes"
)

func main() {
	src := flag.String("source", "docker", "collector to run (docker, kubernetes)")
	socket := flag.String("docker-socket", docker.DefaultSocket, "Docker socket path")
	kubeCtx := flag.String("kube-context", "", "kubectl context (default: current)")
	window := flag.Duration("since", time.Hour, "how far back to look")
	flag.Parse()

	var c collector.Collector
	switch *src {
	case "docker":
		c = docker.New(*socket)
	case "kubernetes":
		c = kubernetes.New(*kubeCtx)
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
