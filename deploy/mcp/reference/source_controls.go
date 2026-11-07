package reference

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// SetSourceAvailable controls a reference source fixture. Metadata and gateway
// services stay up during a source outage.
func (env *Environment) SetSourceAvailable(ctx context.Context, sourceKey string, available bool) error {
	for _, def := range sourceDefinitions {
		if def.key != sourceKey {
			continue
		}
		if available {
			return env.stack.up(ctx, def.service)
		}
		_, err := env.stack.compose(ctx, "stop", def.service)
		return err
	}
	if sourceKey == "trino" && env.stack.trinoIntegration {
		if !available {
			_, err := env.stack.compose(ctx, "stop", "trino")
			return err
		}
		if err := env.stack.up(ctx, "trino"); err != nil {
			return err
		}
		var err error
		env.TrinoCoordinatorURL, err = env.stack.url(ctx, "trino", "8080", "http")
		return err
	}
	return fmt.Errorf("unknown reference source")
}

// WithPostgresOrdersLock holds a lock in an independent source-side
// transaction. The application sessions still query via the gateway. Closing
// psql's input rolls the transaction back, including on check failure.
func (env *Environment) WithPostgresOrdersLock(ctx context.Context, check func(context.Context) error) (err error) {
	args := env.stack.composeArgs("exec", "-T", "sample-postgres",
		"psql", "-XqAt", "-U", "postgres", "-d", "sample", "-v", "ON_ERROR_STOP=1")
	cmd := env.stack.command(ctx, args...)
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		in.Close()
		if waitErr := cmd.Wait(); err == nil && waitErr != nil {
			err = fmt.Errorf("release synthetic PostgreSQL lock: %w", waitErr)
		}
	}()

	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(out).ReadString('\n')
		ready <- strings.TrimSpace(line)
	}()
	const lockSQL = "BEGIN;\nSET LOCAL lock_timeout = '5s';\nLOCK TABLE public.orders IN ACCESS EXCLUSIVE MODE;\nSELECT 'reference-lock-held';\n"
	if _, err := io.WriteString(in, lockSQL); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case marker := <-ready:
		if marker != "reference-lock-held" {
			return fmt.Errorf("could not acquire synthetic PostgreSQL lock")
		}
	}
	return check(ctx)
}

// AssertLogsOmit checks the two synthetic gateway processes without returning
// any raw log lines or matching values to the caller.
func (env *Environment) AssertLogsOmit(ctx context.Context, values ...string) error {
	args := env.stack.composeArgs("logs", "--no-color",
		"data-fabric-api", "data-fabric-api-b",
		"data-fabric-mcp", "data-fabric-mcp-b",
		"replica-ingress")
	rawLogs, err := env.stack.command(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("read reference gateway logs: %w", err)
	}
	logs := string(rawLogs)
	if logs == "" {
		return fmt.Errorf("reference gateway logs are unavailable")
	}
	for _, value := range values {
		if value != "" && strings.Contains(logs, value) {
			return fmt.Errorf("reference gateway logs contain a sensitive value")
		}
	}
	return nil
}
