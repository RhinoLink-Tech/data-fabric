package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	if err := reference.RecoveryAcceptance(ctx, "", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Recovery acceptance failed:", err)
		os.Exit(1)
	}
}
