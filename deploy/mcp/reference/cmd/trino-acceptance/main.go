package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference/acceptance"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	options := reference.Options{Output: os.Stderr, TrinoIntegration: true}
	if err := reference.Run(ctx, options, func(ctx context.Context, env *reference.Environment) error {
		return acceptance.CheckTrino(ctx, env, os.Stdout)
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
