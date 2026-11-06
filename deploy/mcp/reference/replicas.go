package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

type replicaProfile struct {
	deployment string
	ring       *replicarouting.KeyRing
	path       string
	upstreams  replicarouting.Upstreams
}

func (env *Environment) configureReplicaRouting() error {
	deployment := env.stack.env["ROUTING_DEPLOYMENT_ID"]
	keys := env.stack.env["DATA_FABRIC_ROUTING_KEYS"]
	ring, err := replicarouting.ParseKeyRing(deployment, keys)
	if err != nil {
		return err
	}
	env.replica = &replicaProfile{
		deployment: deployment,
		ring:       ring,
		path:       filepath.Join(env.stack.directory, "routing.json"),
		upstreams: replicarouting.Upstreams{
			REST: map[string]replicarouting.Upstream{
				"rest-a": {URL: "http://data-fabric-api:8080", AcceptNew: true},
				"rest-b": {URL: "http://data-fabric-api-b:8080"},
			},
			MCP: map[string]replicarouting.Upstream{
				"mcp-a": {URL: "https://data-fabric-mcp:8081", AcceptNew: true},
				"mcp-b": {URL: "https://data-fabric-mcp-b:8081"},
			},
		},
	}
	return env.writeReplicaRouting()
}

func (env *Environment) writeReplicaRouting() error {
	if env.replica == nil {
		return fmt.Errorf("replica routing is not configured")
	}
	data, err := json.MarshalIndent(map[string]any{
		"deployment_id":  env.replica.deployment,
		"rest_upstreams": env.replica.upstreams.REST,
		"mcp_upstreams":  env.replica.upstreams.MCP,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(env.replica.path, data, 0o644)
}

// SetRoutedNewOwner changes only which replica accepts new work. Existing
// REST continuations and MCP sessions remain routable by their signed marker.
func (env *Environment) SetRoutedNewOwner(ctx context.Context, pool, slot string) error {
	if env.replica == nil {
		return fmt.Errorf("replica routing is not configured")
	}
	var entries map[string]replicarouting.Upstream
	switch pool {
	case replicarouting.REST:
		entries = env.replica.upstreams.REST
	case replicarouting.MCP:
		entries = env.replica.upstreams.MCP
	default:
		return fmt.Errorf("unknown replica pool")
	}
	if _, ok := entries[slot]; !ok {
		return fmt.Errorf("unknown owner slot")
	}
	for name, entry := range entries {
		entry.AcceptNew = name == slot
		entries[name] = entry
	}
	if err := env.writeReplicaRouting(); err != nil {
		return fmt.Errorf("write replica routing map: %w", err)
	}
	before, err := env.stack.compose(ctx, "logs", "--no-color", "--no-log-prefix", "replica-ingress")
	if err != nil {
		return err
	}
	previousReloads := strings.Count(before, "Reloaded routing upstreams")
	if _, err := env.stack.compose(ctx, "kill", "--signal", "HUP", "replica-ingress"); err != nil {
		return err
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		logs, logErr := env.stack.compose(ctx, "logs", "--no-color", "--no-log-prefix", "replica-ingress")
		if logErr == nil && strings.Count(logs, "Reloaded routing upstreams") > previousReloads {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if logErr != nil {
				return fmt.Errorf("routing reload was not observed: %w", logErr)
			}
			return fmt.Errorf("routing reload was not observed")
		case <-ticker.C:
		}
	}
}

// RoutedOwner verifies the signed marker using this profile's test-only key.
func (env *Environment) RoutedOwner(marker, pool, id string) (string, error) {
	if env.replica == nil {
		return "", fmt.Errorf("replica routing is not configured")
	}
	return env.replica.ring.Verify(marker, pool, id, time.Now())
}
