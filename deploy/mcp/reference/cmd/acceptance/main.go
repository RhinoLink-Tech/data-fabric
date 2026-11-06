package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference/acceptance"
)

func main() {
	var options reference.Options
	flag.StringVar(&options.RepoRoot, "repo", "", "repository root (defaults to the working directory or its ancestor)")
	flag.StringVar(&options.Image, "image", "", "use a prebuilt local Data Fabric image instead of building")
	flag.BoolVar(&options.Keep, "keep", false, "keep the isolated reference stack for inspection")
	smoke := flag.Bool("smoke", false, "run the premerge subset of public acceptance cases")
	flag.Parse()
	options.Output = os.Stderr
	if !*smoke {
		options.SessionTTLSeconds = acceptance.SessionTTLSeconds
		options.MaxConcurrentQueriesPerTenant = acceptance.TenantCapacity
		fmt.Fprintf(os.Stderr, "Public acceptance profile full: query session TTL %ds, tenant capacity %d\n",
			options.SessionTTLSeconds, options.MaxConcurrentQueriesPerTenant)
	} else {
		fmt.Fprintln(os.Stderr, "Public acceptance profile smoke with default reference query settings")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := reference.Run(ctx, options, func(ctx context.Context, env *reference.Environment) error {
		return acceptance.Check(ctx, env, os.Stdout, *smoke)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
