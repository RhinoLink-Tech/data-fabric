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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	image := flag.String("image", "", "existing gateway image; avoids building a new one")
	noPull := flag.Bool("no-pull", false, "use cached images only; requires -image")
	federatedOnly := flag.Bool("federated-only", false, "run the MySQL/PostgreSQL join and denial scenarios")
	flag.Parse()
	options := reference.Options{Output: os.Stderr, TrinoIntegration: true, Image: *image, NoPull: *noPull}
	if err := reference.Run(ctx, options, func(ctx context.Context, env *reference.Environment) error {
		if *federatedOnly {
			return acceptance.CheckFederated(ctx, env, os.Stdout)
		}
		return acceptance.CheckTrino(ctx, env, os.Stdout)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
