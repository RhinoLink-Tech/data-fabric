package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

func main() {
	var options reference.Options
	flag.StringVar(&options.RepoRoot, "repo", "", "repository root (defaults to the working directory or its ancestor)")
	flag.StringVar(&options.Image, "image", "", "use a prebuilt local Data Fabric image instead of building")
	flag.BoolVar(&options.Keep, "keep", false, "keep the reference stack and generated local configuration for inspection")
	flag.Parse()
	options.Output = os.Stdout

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := reference.Run(ctx, options, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
