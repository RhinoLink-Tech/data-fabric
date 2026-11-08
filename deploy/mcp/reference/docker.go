package reference

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	mysqlImage    = "mysql:8.4.13@sha256:a0225d1ea6631c8bc2385dc4c2faba17bbf1834ecf369e1659a9a382bd8d5dd7"
	postgresImage = "postgres:17-alpine@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24"
	trinoImage    = "trinodb/trino:484@sha256:2704eb53935af5e582d63d6033e9bf99d20c325e9b0e30b1016ef59a4773979f"
)

type composeStack struct {
	root             string
	directory        string
	project          string
	image            string
	env              map[string]string
	redact           *strings.Replacer
	multiReplica     bool
	trinoIntegration bool
}

func (s *composeStack) composeArgs(args ...string) []string {
	files := []string{
		"compose", "--project-name", s.project, "--env-file", filepath.Join(s.directory, "compose.env"),
		"-f", filepath.Join(s.root, "deploy", "mcp", "docker-compose.yml"),
		"-f", filepath.Join(s.root, "deploy", "mcp", "docker-compose.reference.yml"),
	}
	if s.multiReplica {
		files = append(files, "-f", filepath.Join(s.root, "deploy", "mcp", "docker-compose.reference-replicas.yml"))
	}
	if s.trinoIntegration {
		files = append(files, "-f", filepath.Join(s.root, "deploy", "mcp", "docker-compose.reference-trino.yml"))
	}
	return append(files, args...)
}

func (s *composeStack) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = s.root
	// Shell variables take priority over --env-file in Compose. Remove only
	// the keys owned by the reference profile, preserving Docker settings.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, owned := s.env[strings.ToUpper(key)]; !owned {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	return cmd
}

func (s *composeStack) docker(ctx context.Context, args ...string) (string, error) {
	cmd := s.command(ctx, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(stdout)), nil
	}
	message := s.redact.Replace(strings.TrimSpace(stderr.String()))
	if len(message) > 1500 {
		message = message[len(message)-1500:]
	}
	return "", fmt.Errorf("docker %s failed: %w: %s", args[0], err, message)
}

func (s *composeStack) compose(ctx context.Context, args ...string) (string, error) {
	return s.docker(ctx, s.composeArgs(args...)...)
}

func (s *composeStack) up(ctx context.Context, services ...string) error {
	_, err := s.compose(ctx, append([]string{"up", "-d", "--wait", "--wait-timeout", "180", "--no-build"}, services...)...)
	return err
}

func (s *composeStack) url(ctx context.Context, service, port, scheme string) (string, error) {
	address, err := s.compose(ctx, "port", service, port)
	if err != nil {
		return "", err
	}
	host, published, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" || published == "" {
		return "", fmt.Errorf("%s did not publish its port to 127.0.0.1", service)
	}
	return scheme + "://" + address, nil
}

func (s *composeStack) writeEnvironment() error {
	keys := make([]string, 0, len(s.env))
	for key := range s.env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var envFile strings.Builder
	for _, key := range keys {
		value := s.env[key]
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("invalid reference configuration for %s", key)
		}
		fmt.Fprintf(&envFile, "%s='%s'\n", key, strings.ReplaceAll(value, "'", "\\'"))
	}
	return os.WriteFile(filepath.Join(s.directory, "compose.env"), []byte(envFile.String()), 0o600)
}

func (s *composeStack) cleanup(output io.Writer, keep, ownedImage bool) error {
	if keep {
		fmt.Fprintf(output, "Kept reference project %s. Compose environment: %s\n", s.project, filepath.Join(s.directory, "compose.env"))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if _, err := s.compose(ctx, "down", "--volumes", "--remove-orphans"); err != nil {
		fmt.Fprintf(output, "Cleanup needs a retry for project %s. Compose environment: %s\n", s.project, filepath.Join(s.directory, "compose.env"))
		return err
	}
	// This image tag is unique to the run; never delete a supplied image.
	if ownedImage {
		if _, err := s.docker(ctx, "image", "inspect", s.image); err == nil {
			if _, err := s.docker(ctx, "image", "rm", s.image); err != nil {
				return errors.Join(err, os.RemoveAll(s.directory))
			}
		}
	}
	return os.RemoveAll(s.directory)
}

func (s *composeStack) resourceSnapshot(ctx context.Context) (ResourceSnapshot, error) {
	raw, err := s.compose(ctx, "ps", "--quiet")
	if err != nil {
		return ResourceSnapshot{}, err
	}
	ids := strings.Fields(raw)
	expected := 6
	if s.multiReplica {
		expected = 9
	}
	if s.trinoIntegration {
		expected++
	}
	if len(ids) != expected {
		return ResourceSnapshot{}, fmt.Errorf("resource snapshot expected %d reference containers, got %d", expected, len(ids))
	}
	stats, err := s.docker(ctx, append([]string{
		"stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}",
	}, ids...)...)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	snapshot := ResourceSnapshot{CapturedAtUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, line := range strings.Split(stats, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return ResourceSnapshot{}, fmt.Errorf("unexpected docker stats output")
		}
		cpuText := strings.TrimSuffix(strings.TrimSpace(fields[1]), "%")
		cpu, err := strconv.ParseFloat(cpuText, 64)
		if err != nil {
			return ResourceSnapshot{}, fmt.Errorf("parse docker CPU sample: %w", err)
		}
		memoryText, limitText, ok := strings.Cut(fields[2], "/")
		if !ok {
			return ResourceSnapshot{}, fmt.Errorf("unexpected docker memory sample")
		}
		memory, err := memoryBytes(strings.TrimSpace(memoryText))
		if err != nil {
			return ResourceSnapshot{}, err
		}
		limit, err := memoryBytes(strings.TrimSpace(limitText))
		if err != nil {
			return ResourceSnapshot{}, err
		}
		container := ContainerResource{
			Name: strings.TrimSpace(fields[0]), CPUPercent: cpu,
			MemoryBytes: uint64(memory), MemoryLimitBytes: uint64(limit),
		}
		snapshot.Containers = append(snapshot.Containers, container)
		snapshot.TotalMemoryBytes += container.MemoryBytes
	}
	return snapshot, nil
}

func (s *composeStack) recordResources(ctx context.Context, out io.Writer) error {
	snapshot, err := s.resourceSnapshot(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Container snapshot (name, CPU, memory/limit):")
	for _, container := range snapshot.Containers {
		fmt.Fprintf(out, "%s %.2f%% %.2fMiB / %.2fMiB\n", container.Name,
			container.CPUPercent, float64(container.MemoryBytes)/(1<<20),
			float64(container.MemoryLimitBytes)/(1<<20))
	}
	fmt.Fprintf(out, "Combined container memory: %.2f MiB\n",
		float64(snapshot.TotalMemoryBytes)/(1<<20))
	return nil
}

func memoryBytes(value string) (float64, error) {
	for _, unit := range []struct {
		name string
		mult float64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"B", 1}} {
		if number, ok := strings.CutSuffix(value, unit.name); ok {
			n, err := strconv.ParseFloat(number, 64)
			return n * unit.mult, err
		}
	}
	return 0, fmt.Errorf("unrecognized docker stats memory unit")
}
