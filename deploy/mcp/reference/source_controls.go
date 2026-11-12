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
	services := []string{"data-fabric-api", "data-fabric-mcp"}
	if env.stack.multiReplica {
		services = append(services, "data-fabric-api-b", "data-fabric-mcp-b", "replica-ingress")
	}
	args := env.stack.composeArgs(append([]string{"logs", "--no-color"}, services...)...)
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

// OrdersFixtureMutation is a closed set of changes for the disposable
// reference orders table. It cannot target arbitrary SQL or a customer source.
type OrdersFixtureMutation string

const (
	InsertOrderBeforeFirst OrdersFixtureMutation = "insert-before-first"
	DeleteFixtureOrder     OrdersFixtureMutation = "delete-fixture-order"
	MoveFixtureOrder       OrdersFixtureMutation = "move-fixture-order"
	ShipFixtureOrder       OrdersFixtureMutation = "ship-fixture-order"
)

// MutateSyntheticOrders applies one bounded mutation to this run's MySQL or
// PostgreSQL fixture. The SQL is selected from a closed set; the only numeric
// input is checked against the fixture IDs before it reaches a command.
func (env *Environment) MutateSyntheticOrders(ctx context.Context, sourceKey string, mutation OrdersFixtureMutation, id int64) error {
	if env == nil || env.stack == nil {
		return fmt.Errorf("reference source fixture is unavailable")
	}
	var statement string
	switch mutation {
	case InsertOrderBeforeFirst:
		if id != 0 {
			return fmt.Errorf("insert fixture order requires ID zero")
		}
		statement = "INSERT INTO orders (id, customer, amount, status, created_at) VALUES (0, 'Synthetic Insert', 1.00, 'new', '2026-11-08 00:00:00')"
	case DeleteFixtureOrder:
		if id != 3 {
			return fmt.Errorf("only the unreferenced seeded order may be deleted")
		}
		statement = "DELETE FROM orders WHERE id = 3"
	case MoveFixtureOrder:
		if id != 0 {
			return fmt.Errorf("move fixture order uses its fixed seeded ID")
		}
		statement = "UPDATE orders SET id = 4 WHERE id = 3"
	case ShipFixtureOrder:
		if id != 0 {
			return fmt.Errorf("status fixture mutation uses its fixed seeded ID")
		}
		statement = "UPDATE orders SET status = 'shipped' WHERE id = 2"
	default:
		return fmt.Errorf("unsupported synthetic orders mutation")
	}

	var service, command string
	switch sourceKey {
	case "mysql":
		service = "sample-source"
		command = "MYSQL_PWD=\"$MYSQL_ROOT_PASSWORD\" mysql -uroot --database=sample --batch --skip-column-names --execute=\"" + statement + "\""
	case "postgresql":
		service = "sample-postgres"
		command = "PGPASSWORD=\"$POSTGRES_PASSWORD\" psql -XqAt -U postgres -d sample -v ON_ERROR_STOP=1 -c \"" + statement + "\""
	default:
		return fmt.Errorf("unsupported reference source")
	}
	if _, err := env.stack.compose(ctx, "exec", "-T", service, "sh", "-lc", command); err != nil {
		return fmt.Errorf("mutate disposable %s orders fixture: %w", sourceKey, err)
	}
	return nil
}

// ResetSyntheticOrders restores only the three seeded orders in this run's
// disposable reference source, leaving other fixture tables untouched.
func (env *Environment) ResetSyntheticOrders(ctx context.Context, sourceKey string) error {
	if env == nil || env.stack == nil {
		return fmt.Errorf("reference source fixture is unavailable")
	}
	var service, command string
	switch sourceKey {
	case "mysql":
		service = "sample-source"
		statement := "DELETE FROM orders WHERE id = 0 AND customer = 'Synthetic Insert'; UPDATE orders SET id = 3 WHERE id = 4; INSERT INTO orders (id, customer, amount, status, created_at) VALUES (3, 'Alan Turing', 1200.00, 'shipped', '2026-10-12 08:05:00') ON DUPLICATE KEY UPDATE customer = 'Alan Turing', amount = 1200.00, status = 'shipped', created_at = '2026-10-12 08:05:00'; UPDATE orders SET customer = 'Ada Lovelace', amount = 250.00, status = 'shipped', created_at = '2026-10-01 09:15:00' WHERE id = 1; UPDATE orders SET customer = 'Grace Hopper', amount = 89.50, status = 'new', created_at = '2026-10-05 14:30:00' WHERE id = 2"
		command = "MYSQL_PWD=\"$MYSQL_ROOT_PASSWORD\" mysql -uroot --database=sample --batch --skip-column-names --execute=\"" + statement + "\""
	case "postgresql":
		service = "sample-postgres"
		statement := "DELETE FROM public.orders WHERE id = 0 AND customer = 'Synthetic Insert'; UPDATE public.orders SET id = 3 WHERE id = 4; INSERT INTO public.orders (id, customer, amount, status, created_at) VALUES (3, 'Alan Turing', 1200.00, 'shipped', '2026-10-12 08:05:00') ON CONFLICT (id) DO UPDATE SET customer = 'Alan Turing', amount = 1200.00, status = 'shipped', created_at = '2026-10-12 08:05:00'; UPDATE public.orders SET customer = 'Ada Lovelace', amount = 250.00, status = 'shipped', created_at = '2026-10-01 09:15:00' WHERE id = 1; UPDATE public.orders SET customer = 'Grace Hopper', amount = 89.50, status = 'new', created_at = '2026-10-05 14:30:00' WHERE id = 2"
		command = "PGPASSWORD=\"$POSTGRES_PASSWORD\" psql -XqAt -U postgres -d sample -v ON_ERROR_STOP=1 -c \"" + statement + "\""
	default:
		return fmt.Errorf("unsupported reference source")
	}
	if _, err := env.stack.compose(ctx, "exec", "-T", service, "sh", "-lc", command); err != nil {
		return fmt.Errorf("reset disposable %s orders fixture: %w", sourceKey, err)
	}
	return nil
}
