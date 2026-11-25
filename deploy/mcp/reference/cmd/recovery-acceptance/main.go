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
	publishedRelease := flag.String("published-release", "", "test a supported published release by tag")
	releaseArchive := flag.String("release-archive", "", "path to the selected published source archive")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	var err error
	if *publishedRelease != "" || *releaseArchive != "" {
		release, releaseErr := reference.PublishedReleaseByTag(*publishedRelease)
		if releaseErr != nil {
			err = releaseErr
		} else if *releaseArchive == "" {
			err = fmt.Errorf("published source archive is required")
		} else {
			err = reference.PublishedRecoveryAcceptance(ctx, "", *releaseArchive, release, os.Stdout)
		}
	} else {
		err = reference.RecoveryAcceptance(ctx, "", os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Recovery acceptance failed:", err)
		os.Exit(1)
	}
}
