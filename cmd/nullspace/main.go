// Package main provides the nullspace CLI and MCP server executable.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/carlpatchett/nullspace/pkg/indexer"
	"github.com/carlpatchett/nullspace/pkg/mcp"
	"github.com/carlpatchett/nullspace/pkg/telemetry"
)

func main() {
	var rootFlag string
	var traceFlag string
	var excludeFlag string
	var modeFlag string

	flag.StringVar(&rootFlag, "root", "", "Workspace root directory (or NULLSPACE_ROOT env)")
	flag.StringVar(&traceFlag, "trace", "", "OpenTelemetry JSONL trace log path (or NULLSPACE_TRACE_PATH env)")
	flag.StringVar(&excludeFlag, "exclude", "", "Comma-separated directory names to exclude (or NULLSPACE_EXCLUDES env)")
	flag.StringVar(&modeFlag, "mode", "serve", "Operating mode: serve (MCP stdio), prune, check, stats")
	flag.Parse()

	// Environment variable overrides
	if rootFlag == "" {
		rootFlag = os.Getenv("NULLSPACE_ROOT")
	}
	if rootFlag == "" {
		rootFlag = "."
	}

	if traceFlag == "" {
		traceFlag = os.Getenv("NULLSPACE_TRACE_PATH")
	}
	if traceFlag == "" {
		traceFlag = "null_space_trace.jsonl"
	}

	excludes := indexer.DefaultExcludes
	if excludeFlag == "" {
		excludeFlag = os.Getenv("NULLSPACE_EXCLUDES")
	}
	if excludeFlag != "" {
		for _, e := range strings.Split(excludeFlag, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				excludes = append(excludes, e)
			}
		}
	}

	tel := telemetry.New(telemetry.Config{
		ServiceName: "nullspace",
		TracePath:   traceFlag,
	})
	defer func() { _ = tel.Close() }()
	cfg := indexer.DefaultConfig(rootFlag)
	cfg.Excludes = excludes
	cfg.Telemetry = tel

	idx, err := indexer.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nullspace: error initializing index: %v\n", err)
		os.Exit(1)
	}

	args := flag.Args()
	subcommand := modeFlag
	if len(args) > 0 && (args[0] == "prune" || args[0] == "check" || args[0] == "stats" || args[0] == "serve") {
		subcommand = args[0]
		args = args[1:]
	}

	switch subcommand {
	case "prune":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: nullspace prune <token> [--case-sensitive]")
			os.Exit(1)
		}
		token := args[0]
		caseSensitive := len(args) > 1 && (args[1] == "--case-sensitive" || args[1] == "-c")
		res, err := idx.PruneSearchSpace(token, caseSensitive)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(data))

	case "check":
		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: nullspace check <token> [path]")
			os.Exit(1)
		}
		token := args[0]
		targetPath := "."
		if len(args) > 1 {
			targetPath = args[1]
		}
		res, err := idx.CheckToken(token, targetPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(data))

	case "stats":
		stats, err := idx.Stats()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(stats, "", "  ")
		fmt.Println(string(data))

	default: // "serve" or default
		if err := mcp.RunStdio(idx, tel); err != nil {
			fmt.Fprintf(os.Stderr, "nullspace MCP server error: %v\n", err)
			os.Exit(1)
		}
	}
}
