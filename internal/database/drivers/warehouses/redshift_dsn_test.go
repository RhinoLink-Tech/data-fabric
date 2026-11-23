package warehouses

import (
	"context"
	"encoding/binary"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/lib/pq"
	"io"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRedshiftDSNRoundTripsThroughPQ(t *testing.T) {
	for _, tc := range []struct{ name, host, user, password, database string }{
		{"standard", "warehouse.example", "reader", "normal", "analytics"},
		{"special", "::1", `name ' \ x`, `p ' \ &?=`, `sales / ' \ x`},
		{"parameter injection", "warehouse.example", "reader", `secret sslmode=disable`, "analytics"},
		{"empty credentials", "warehouse.example", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &model.DataSourceConfig{Host: tc.host, Port: 5439, Username: tc.user, Password: tc.password, Database: tc.database}
			dsn := (&RedshiftDriver{}).BuildDSN(cfg)
			if _, err := pq.NewConnector(dsn); err != nil {
				t.Fatal("lib/pq rejected connection options")
			}
			u := url.URL{Scheme: "postgres", User: url.UserPassword(tc.user, tc.password), Host: "example:5439", Path: "/" + tc.database, RawPath: "/" + url.PathEscape(tc.database)}
			parsed, err := pq.ParseURL(u.String())
			if err != nil {
				t.Fatal("lib/pq URL reference rejected")
			}
			escape := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
			for key, want := range map[string]string{"user": tc.user, "password": tc.password, "dbname": tc.database} {
				expected := key + "='" + escape.Replace(want) + "'"
				if !strings.Contains(dsn, expected) || (want != "" && !strings.Contains(parsed, expected)) {
					t.Fatalf("connection field %s did not round-trip", key)
				}
			}
		})
	}
}
func TestRedshiftDSNRejectsInvalidHostOrPort(t *testing.T) {
	d := &RedshiftDriver{}
	for _, cfg := range []*model.DataSourceConfig{nil, {Host: "host?sslmode=disable", Port: 5439}, {Host: "host", Port: 0}, {Host: "host", Port: 65536}} {
		if d.BuildDSN(cfg) != "" {
			t.Fatal("invalid configuration produced a connection string")
		}
	}
}

// Exercise lib/pq's actual startup and cleartext-auth protocol through an
// in-memory connection. All credentials are synthetic and never logged.
type redshiftFixtureDialer struct{ conn net.Conn }

func (d redshiftFixtureDialer) Dial(string, string) (net.Conn, error) { return d.conn, nil }
func (d redshiftFixtureDialer) DialTimeout(string, string, time.Duration) (net.Conn, error) {
	return d.conn, nil
}

type redshiftFixtureResult struct {
	user, database, password string
	err                      error
}

func TestRedshiftDSNPreservesPQStartupAndPassword(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	result := make(chan redshiftFixtureResult, 1)
	go func() {
		outcome := redshiftFixtureResult{}
		defer func() { result <- outcome }()
		var length uint32
		if outcome.err = binary.Read(server, binary.BigEndian, &length); outcome.err != nil {
			return
		}
		if length < 8 || length > 8192 {
			outcome.err = io.ErrUnexpectedEOF
			return
		}
		startup := make([]byte, length-4)
		if _, outcome.err = io.ReadFull(server, startup); outcome.err != nil {
			return
		}
		parts := strings.Split(string(startup[4:]), "\x00")
		for i := 0; i+1 < len(parts); i += 2 {
			if parts[i] == "user" {
				outcome.user = parts[i+1]
			}
			if parts[i] == "database" {
				outcome.database = parts[i+1]
			}
		}
		if _, outcome.err = server.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 3}); outcome.err != nil {
			return
		}
		header := make([]byte, 5)
		if _, outcome.err = io.ReadFull(server, header); outcome.err != nil {
			return
		}
		size := binary.BigEndian.Uint32(header[1:])
		if header[0] != 'p' || size < 5 || size > 8192 {
			outcome.err = io.ErrUnexpectedEOF
			return
		}
		pw := make([]byte, size-4)
		if _, outcome.err = io.ReadFull(server, pw); outcome.err != nil {
			return
		}
		outcome.password = strings.TrimSuffix(string(pw), "\x00")
		server.Close()
	}()
	cfg := &model.DataSourceConfig{Host: "fixture.invalid", Port: 5439, Username: `name ' \ x`, Password: `p ' \ sslmode=disable`, Database: `sales ' \ x`}
	// TLS is disabled only for the in-memory fixture; BuildDSN never changes it.
	connector, err := pq.NewConnector((&RedshiftDriver{}).BuildDSN(cfg) + " sslmode=disable")
	if err != nil {
		t.Fatal("fixture connector rejected")
	}
	connector.Dialer(redshiftFixtureDialer{client})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = connector.Connect(ctx)
	_ = client.Close()
	select {
	case got := <-result:
		if got.err != nil || got.user != cfg.Username || got.database != cfg.Database || got.password != cfg.Password {
			t.Fatal("lib/pq did not preserve startup fields and password")
		}
	case <-ctx.Done():
		t.Fatal("lib/pq fixture timed out")
	}
}
