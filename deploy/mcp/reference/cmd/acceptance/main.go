package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference/acceptance"
)

func main() {
	var options reference.Options
	flag.StringVar(&options.RepoRoot, "repo", "", "repository root (defaults to the working directory or its ancestor)")
	flag.StringVar(&options.Image, "image", "", "use a prebuilt local Data Fabric image instead of building")
	flag.BoolVar(&options.Keep, "keep", false, "keep the isolated reference stack for inspection")
	smoke := flag.Bool("smoke", false, "run the premerge subset of public acceptance cases")
	multiReplica := flag.Bool("multi-replica", false, "run the containerized two-replica continuation acceptance cases")
	benchmarkSamples := flag.Int("benchmark-samples", 0, "also measure warmed serial REST/MCP tasks (minimum 20 samples)")
	benchmarkOutput := flag.String("benchmark-output", "", "write benchmark raw samples and summaries as JSONL")
	flag.Parse()
	if *benchmarkSamples < 0 || (*benchmarkSamples > 0 && *benchmarkSamples < 20) ||
		(*benchmarkSamples == 0) != (*benchmarkOutput == "") {
		fmt.Fprintln(os.Stderr, "benchmark requires both -benchmark-samples (20 or more) and -benchmark-output")
		os.Exit(2)
	}
	options.Output = os.Stderr
	options.MultiReplica = *multiReplica
	options.SessionTTLSeconds = acceptance.SessionTTLSeconds
	options.MaxConcurrentQueriesPerTenant = acceptance.TenantCapacity
	if *smoke {
		fmt.Fprintf(os.Stderr, "Public acceptance profile smoke: query session TTL %ds, tenant capacity %d\n",
			options.SessionTTLSeconds, options.MaxConcurrentQueriesPerTenant)
	} else {
		fmt.Fprintf(os.Stderr, "Public acceptance profile full: query session TTL %ds, tenant capacity %d\n",
			options.SessionTTLSeconds, options.MaxConcurrentQueriesPerTenant)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := reference.Run(ctx, options, func(ctx context.Context, env *reference.Environment) error {
		var failures []error
		if err := acceptance.Check(ctx, env, os.Stdout, *smoke); err != nil {
			failures = append(failures, err)
		}
		if *multiReplica {
			if err := acceptance.CheckMultiReplica(ctx, env, os.Stdout); err != nil {
				failures = append(failures, err)
			}
		}
		if *benchmarkSamples > 0 {
			if err := os.MkdirAll(filepath.Dir(*benchmarkOutput), 0o755); err != nil {
				failures = append(failures, fmt.Errorf("create benchmark output directory: %w", err))
			} else {
				output, err := os.Create(*benchmarkOutput)
				if err != nil {
					failures = append(failures, fmt.Errorf("create benchmark output: %w", err))
				} else {
					benchmarkErr := acceptance.Benchmark(ctx, env, output, *benchmarkSamples)
					closeErr := output.Close()
					if benchmarkErr != nil || closeErr != nil {
						failures = append(failures, errors.Join(benchmarkErr, closeErr))
					}
				}
			}
		}
		return errors.Join(failures...)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
