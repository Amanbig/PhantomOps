// Command phantom-scan runs the correlation engine once and prints incidents.
// It scans live infrastructure using the PhantomOps config, or replays a
// recorded events file with -replay (useful for testing and debugging).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"PhantomOps/internal/config"
	"PhantomOps/internal/engine"
	"PhantomOps/internal/llm"
	"PhantomOps/internal/model"
)

func main() {
	cfgPath := flag.String("config", config.Path(), "config file (default $PHANTOM_CONFIG or user config dir)")
	replay := flag.String("replay", "", "analyze events from this JSON file instead of scanning live")
	asJSON := flag.Bool("json", false, "print the full result as JSON")
	ignore := flag.String("ignore", "", "comma-separated subject substrings to ignore (added to config)")
	aiURL := flag.String("ai-url", "", "OpenAI-compatible base URL, e.g. http://localhost:11434/v1 (overrides config)")
	aiModel := flag.String("ai-model", "", "model name for AI refinement (overrides config)")
	aiKey := flag.String("ai-key", os.Getenv("PHANTOM_AI_KEY"), "API key for the AI endpoint (default: $PHANTOM_AI_KEY)")
	flag.Parse()

	cfg, _, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	for _, s := range strings.Split(*ignore, ",") {
		if s = strings.TrimSpace(s); s != "" {
			cfg.IgnoreSubjects = append(cfg.IgnoreSubjects, s)
		}
	}
	eng, notes := cfg.Engine()
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, "config note:", n)
	}

	var res engine.Result
	if *replay != "" {
		b, err := os.ReadFile(*replay)
		if err != nil {
			fmt.Fprintln(os.Stderr, "replay:", err)
			os.Exit(2)
		}
		var events []model.Event
		if err := json.Unmarshal(b, &events); err != nil {
			fmt.Fprintln(os.Stderr, "replay: invalid events file:", err)
			os.Exit(2)
		}
		res.Incidents, res.Warnings = eng.Analyze(events)
		res.ScannedAt = time.Now().UTC()
		res.Collectors = []string{"replay:" + *replay}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		res = eng.Scan(ctx)
	}

	if *aiURL != "" || *aiModel != "" {
		cfg.AI.Enabled = true
		if *aiURL != "" {
			cfg.AI.BaseURL = *aiURL
		}
		if *aiModel != "" {
			cfg.AI.Model = *aiModel
		}
		if *aiKey != "" {
			cfg.AI.APIKey = *aiKey
		}
	}
	if ai := cfg.Investigator(); ai != nil {
		refineAll(&res, ai)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return
	}
	printResult(res)
}

// refineAll replaces each incident's investigation with the AI-refined one.
// Failures keep the rule-based result and are reported on stderr.
func refineAll(res *engine.Result, ai *llm.Investigator) {
	for i := range res.Incidents {
		inc := &res.Incidents[i]
		ctx, cancel := context.WithTimeout(context.Background(), llm.Timeout)
		start := time.Now()
		inv, err := ai.Refine(ctx, *inc)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "AI (%s) failed for #%s after %s: %v\n", ai.Model(), inc.ID, time.Since(start).Round(time.Second), err)
			continue
		}
		fmt.Fprintf(os.Stderr, "AI (%s) analysed #%s in %s\n", ai.Model(), inc.ID, time.Since(start).Round(time.Second))
		inc.Investigation = inv
	}
}

func printResult(res engine.Result) {
	fmt.Printf("Sources: %s\n", strings.Join(res.Collectors, ", "))
	names := make([]string, 0, len(res.Errors))
	for n := range res.Errors {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Printf("  ! %s failed: %s\n", n, res.Errors[n])
	}
	for _, w := range res.Warnings {
		fmt.Printf("  ~ %s\n", w)
	}
	if len(res.Incidents) == 0 {
		fmt.Println("\nNo incidents detected.")
		return
	}
	for _, inc := range res.Incidents {
		fmt.Printf("\nIncident #%s  [%s]  %s\n  %s\n", inc.ID, strings.ToUpper(string(inc.Severity)), inc.Title, inc.Environment)
		for i, e := range inc.Timeline {
			fmt.Printf("  %d. %s  %-9s %-28s %s\n", i+1, e.Time.UTC().Format("15:04:05"), e.Source, e.Title, e.Subject)
		}
		iv := inc.Investigation
		fmt.Printf("  Root cause (%.0f%%): %s\n", iv.Confidence*100, iv.RootCause)
		for _, r := range iv.Reasoning {
			fmt.Printf("    - %s\n", r)
		}
		if iv.Source == "ai" {
			fmt.Printf("  [AI %s, cites events %s]\n  Rule-based said: %s\n", iv.Model, llm.FormatCitations(iv.CitedEvents), iv.RuleBasedRootCause)
		}
		if iv.AINote != "" {
			fmt.Printf("  Note: %s\n", iv.AINote)
		}
		fmt.Printf("  Recommended: %s\n", iv.RecommendedAction)
		if iv.Action != "" {
			fmt.Printf("  Action: %s -> %s\n", iv.Action, iv.Target)
		}
	}
}
