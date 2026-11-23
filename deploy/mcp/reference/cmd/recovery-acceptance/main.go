package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

func main() {
	preview2Archive := flag.String("preview2-archive", "", "test the published preview.2 archive and image using the current helper")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	var err error
	if *preview2Archive != "" {
		err = reference.Preview2RecoveryAcceptance(ctx, "", *preview2Archive, os.Stdout)
	} else {
		err = reference.RecoveryAcceptance(ctx, "", os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Recovery acceptance failed:", err)
		os.Exit(1)
	}
}
