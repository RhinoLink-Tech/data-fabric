package database

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Executed by the pinned OpenGauss TLS contract runner against an isolated server.
func TestOpenGaussRestrictedOnboardingTLSContract(t *testing.T) {
	portText, ca, badCA := os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PORT"), os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_ROOT_CA"), os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_BAD_CA")
	if portText == "" || ca == "" || badCA == "" {
		t.Skip("OpenGauss TLS contract fixture not configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewFixedIPEgressPolicyWithSourceOptions([]string{"127.0.0.1/32"}, []int{port}, ca, false, true)
	if err != nil {
		t.Fatal(err)
	}
	badPolicy, err := NewFixedIPEgressPolicyWithSourceOptions([]string{"127.0.0.1/32"}, []int{port}, badCA, false, true)
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{ID: "opengauss-contract", Type: model.DatabaseTypeOpenGauss, Config: model.DataSourceConfig{
		Host: "127.0.0.1", Port: port, Database: os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_DATABASE"), Username: os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_USERNAME"), Password: os.Getenv("DATA_FABRIC_OPENGAUSS_TLS_CONTRACT_PASSWORD"), SSL: true, TLSServerName: "localhost", TLSCA: "custom",
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = policy.CheckRestrictedSourceConnectivity(ctx, source); err != nil {
		t.Fatalf("valid TLS probe failed: %v", err)
	}
	pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.CloseAll()
	db, err := pool.GetConnection(ctx, source)
	if err != nil {
		t.Fatalf("restricted pool failed: %v", err)
	}
	var value int
	if err = db.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatalf("query failed: %v", err)
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE should_fail (id INT)"); err == nil {
		t.Fatal("restricted session allowed a write")
	}
	wrong := *source
	wrong.Config.TLSServerName = "wrong.example.test"
	if err = policy.CheckRestrictedSourceConnectivity(ctx, &wrong); err == nil {
		t.Fatal("hostname mismatch accepted")
	}
	if err = badPolicy.CheckRestrictedSourceConnectivity(ctx, source); err == nil {
		t.Fatal("untrusted CA accepted")
	}
	wrong = *source
	wrong.Config.Password = "wrong-synthetic-password"
	if err = policy.CheckRestrictedSourceConnectivity(ctx, &wrong); err == nil {
		t.Fatal("wrong credential accepted")
	}
	wrong = *source
	wrong.Config.Host = "169.254.169.254"
	if err = policy.CheckRestrictedSourceConnectivity(ctx, &wrong); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("unlisted address: %v", err)
	}
	wrong = *source
	wrong.Config.Port = port + 1
	if err = policy.CheckRestrictedSourceConnectivity(ctx, &wrong); !errors.Is(err, ErrEgressDenied) {
		t.Fatalf("unlisted port: %v", err)
	}
}
