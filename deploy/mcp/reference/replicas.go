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

// TestRoutingMarker creates an isolated marker for a case that exercises
// ingress rejection of an otherwise correctly signed deployment or owner slot.
func (env *Environment) TestRoutingMarker(deployment, pool, slot, id string, expiry time.Time) (string, error) {
	if env.replica == nil {
		return "", fmt.Errorf("replica routing is not configured")
	}
	ring, err := replicarouting.ParseKeyRing(deployment, env.stack.env["DATA_FABRIC_ROUTING_KEYS"])
	if err != nil {
		return "", err
	}
	owner, err := ring.NewOwner(env.stack.env["DATA_FABRIC_ROUTING_ACTIVE_KEY_ID"], pool, slot)
	if err != nil {
		return "", err
	}
	return owner.Mark(id, expiry)
}

// RestartReplica replaces one owner process while preserving its configured
// slot so acceptance can prove old boot markers remain unusable.
func (env *Environment) RestartReplica(ctx context.Context, slot string) error {
	owners := map[string]struct {
		service, port, scheme string
	}{
		"rest-a": {"data-fabric-api", "8080", "http"},
		"rest-b": {"data-fabric-api-b", "8080", "http"},
		"mcp-a":  {"data-fabric-mcp", "8081", "https"},
		"mcp-b":  {"data-fabric-mcp-b", "8081", "https"},
	}
	owner, ok := owners[slot]
	if !ok {
		return fmt.Errorf("unknown owner slot")
	}
	if _, err := env.stack.compose(ctx, "restart", owner.service); err != nil {
		return fmt.Errorf("restart %s: %w", slot, err)
	}
	if err := env.stack.up(ctx, owner.service); err != nil {
		return fmt.Errorf("wait for %s: %w", slot, err)
	}
	if env.OwnerURLs != nil {
		address, err := env.stack.url(ctx, owner.service, owner.port, owner.scheme)
		if err != nil {
			return fmt.Errorf("refresh loopback address for %s: %w", slot, err)
		}
		env.OwnerURLs[slot] = address
	}
	return nil
}

// SetRoutedMCPSessionCapacity changes one reference MCP owner's bounded
// session registry for an isolated acceptance case, then restarts that owner.
func (env *Environment) SetRoutedMCPSessionCapacity(ctx context.Context, slot string, maximum int) error {
	services := map[string]string{"mcp-a": "data-fabric-mcp", "mcp-b": "data-fabric-mcp-b"}
	service, ok := services[slot]
	if env.replica == nil || !ok || maximum < 1 {
		return fmt.Errorf("invalid routed MCP capacity target")
	}
	configPath := filepath.Join(env.stack.directory, "configs", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read reference MCP config: %w", err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("decode reference MCP config: %w", err)
	}
	mcpConfig, ok := config["mcp"].(map[string]any)
	if !ok {
		return fmt.Errorf("reference MCP config is missing its MCP section")
	}
	mcpConfig["max_routed_sessions"] = maximum
	updated, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reference MCP config: %w", err)
	}
	if err := os.WriteFile(configPath, updated, 0o644); err != nil {
		return fmt.Errorf("write reference MCP config: %w", err)
	}
	if _, err := env.stack.compose(ctx, "restart", service); err != nil {
		return fmt.Errorf("restart %s with updated session capacity: %w", slot, err)
	}
	if err := env.stack.up(ctx, service); err != nil {
		return fmt.Errorf("wait for %s after capacity change: %w", slot, err)
	}
	if env.OwnerURLs != nil {
		address, err := env.stack.url(ctx, service, "8081", "https")
		if err != nil {
			return fmt.Errorf("refresh loopback address for %s after capacity change: %w", slot, err)
		}
		env.OwnerURLs[slot] = address
	}
	return nil
}

// RetireRoutingKey removes a key from ingress verification and recreates only
// the ingress. Owner processes retain their startup key ring until replaced.
func (env *Environment) RetireRoutingKey(ctx context.Context, keyID string) error {
	if env.replica == nil {
		return fmt.Errorf("replica routing is not configured")
	}
	var keys map[string]string
	if err := json.Unmarshal([]byte(env.stack.env["DATA_FABRIC_ROUTING_KEYS"]), &keys); err != nil {
		return fmt.Errorf("read reference routing keys: %w", err)
	}
	if _, ok := keys[keyID]; !ok {
		return fmt.Errorf("routing key is not configured")
	}
	delete(keys, keyID)
	encoded, err := json.Marshal(keys)
	if err != nil {
		return fmt.Errorf("encode reference routing keys: %w", err)
	}
	env.stack.env["DATA_FABRIC_ROUTING_KEYS"] = string(encoded)
	ring, err := replicarouting.ParseKeyRing(env.replica.deployment, string(encoded))
	if err != nil {
		return err
	}
	env.replica.ring = ring
	if err := env.stack.writeEnvironment(); err != nil {
		return fmt.Errorf("write reference routing keys: %w", err)
	}
	if _, err := env.stack.compose(ctx, "up", "-d", "--no-deps", "--force-recreate", "--wait", "--wait-timeout", "60", "replica-ingress"); err != nil {
		return fmt.Errorf("recreate ingress with retired key: %w", err)
	}
	if err := env.refreshReplicaIngressURL(ctx); err != nil {
		return err
	}
	return nil
}

func (env *Environment) refreshReplicaIngressURL(ctx context.Context) error {
	base, err := env.stack.url(ctx, "replica-ingress", "8082", "http")
	if err != nil {
		return fmt.Errorf("refresh replica ingress address: %w", err)
	}
	env.APIURL = base
	env.MCPURL = base + "/mcp"
	return nil
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

// RoutingDeploymentID returns the synthetic scope used by this reference run.
func (env *Environment) RoutingDeploymentID() string {
	if env.replica == nil {
		return ""
	}
	return env.replica.deployment
}

// MintAcceptanceMarker creates a signed marker for negative ingress tests.
// The key material remains in the runner and is never returned.
func (env *Environment) MintAcceptanceMarker(deployment, keyID, pool, slot, id string, expiry time.Time) (string, error) {
	if env.replica == nil {
		return "", fmt.Errorf("replica routing is not configured")
	}
	ring, err := replicarouting.ParseKeyRing(deployment, env.stack.env["DATA_FABRIC_ROUTING_KEYS"])
	if err != nil {
		return "", err
	}
	owner, err := ring.NewOwner(keyID, pool, slot)
	if err != nil {
		return "", err
	}
	return owner.Mark(id, expiry)
}

// RoutedOwner verifies the signed marker using this profile's test-only key.
func (env *Environment) RoutedOwner(marker, pool, id string) (string, error) {
	if env.replica == nil {
		return "", fmt.Errorf("replica routing is not configured")
	}
	return env.replica.ring.Verify(marker, pool, id, time.Now())
}
