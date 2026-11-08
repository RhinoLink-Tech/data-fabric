// Package reference runs the synthetic, self-hosted Data Fabric reference profile.
package reference

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Reference replica timing and capacity settings are surfaced in acceptance evidence.
const (
	ReferenceOwnerDrainTimeoutSeconds   = 3
	ReferenceRoutedMCPSessionTTLSeconds = 10
	ReferenceMaxRoutedMCPSessions       = 32
)

// Options controls a reference run. All Docker resources are scoped to a unique
// Compose project. The default image is built from RepoRoot.
type Options struct {
	RepoRoot                      string
	Output                        io.Writer
	Keep                          bool
	Image                         string
	SessionTTLSeconds             int
	MaxConcurrentQueriesPerTenant int
	MultiReplica                  bool
	TrinoIntegration              bool
}

type Source struct {
	ID     string
	Type   string
	Schema string
}

// RunInfo identifies the disposable stack and candidate measured by an acceptance run.
type RunInfo struct {
	SourceRevision      string
	WorkingTreeDirty    bool
	DockerServerVersion string
	ComposeVersion      string
	GatewayImage        string
	GatewayImageID      string
	MySQLImage          string
	PostgreSQLImage     string
	SessionTTLSeconds   int
	TenantConcurrency   int
	MultiReplica        bool
	TrinoIntegration    bool
}

// Environment is valid during the Run callback. It uses generated credentials
// and contains only the synthetic sources started by this invocation.
type Environment struct {
	APIURL              string
	MCPURL              string
	TrinoCoordinatorURL string
	TLSConfig           *tls.Config
	RunInfo             RunInfo
	Sources             map[string]Source
	TenantBSources      map[string]Source
	Identities          map[string]Identity
	OwnerURLs           map[string]string

	stack   *composeStack
	restKey string
	oidcKey *rsa.PrivateKey
	replica *replicaProfile
}

// StopMetadataDatabase briefly stops the isolated reference stack's metadata
// database for operational fault-injection acceptance scenarios.
func (env *Environment) StopMetadataDatabase(ctx context.Context) error {
	if env == nil || env.stack == nil {
		return fmt.Errorf("reference metadata database is unavailable")
	}
	if _, err := env.stack.compose(ctx, "stop", "metadata-db"); err != nil {
		return fmt.Errorf("stop reference metadata database: %w", err)
	}
	return nil
}

// StartMetadataDatabase restores the isolated reference stack's metadata
// database and waits for its health check before returning.
func (env *Environment) StartMetadataDatabase(ctx context.Context) error {
	if env == nil || env.stack == nil {
		return fmt.Errorf("reference metadata database is unavailable")
	}
	if err := env.stack.up(ctx, "metadata-db"); err != nil {
		return fmt.Errorf("start reference metadata database: %w", err)
	}
	return nil
}

type sourceDefinition struct {
	key       string
	service   string
	schema    string
	port      int
	readerEnv string
}

var sourceDefinitions = []sourceDefinition{
	{"mysql", "sample-source", "sample", 3306, "SAMPLE_READER_PASSWORD"},
	{"postgresql", "sample-postgres", "public", 5432, "DF_REFERENCE_POSTGRES_READER_PASSWORD"},
}

// Run starts the reference profile, verifies tenant-shared read access across
// both transports, and invokes check before shutting the profile down.
func Run(ctx context.Context, options Options, check func(context.Context, *Environment) error) (err error) {
	start := time.Now()
	out := options.Output
	if out == nil {
		out = io.Discard
	}
	if options.SessionTTLSeconds < 0 || options.SessionTTLSeconds > 1800 {
		return fmt.Errorf("reference session TTL must be between 0 and 1800 seconds")
	}
	if options.MaxConcurrentQueriesPerTenant < 0 || options.MaxConcurrentQueriesPerTenant > 100 {
		return fmt.Errorf("reference per-tenant concurrency must be between 0 and 100")
	}
	root, err := repositoryRoot(options.RepoRoot)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "data-fabric-reference-")
	if err != nil {
		return err
	}
	ownedImage := options.Image == ""
	s := newStack(root, directory, options)
	stackReady := false
	defer func() {
		if stackReady {
			err = errors.Join(err, s.cleanup(out, options.Keep, ownedImage))
		} else {
			err = errors.Join(err, os.RemoveAll(directory))
		}
	}()
	env := &Environment{
		Sources: make(map[string]Source), TenantBSources: make(map[string]Source),
		Identities: make(map[string]Identity), OwnerURLs: make(map[string]string),
		stack: s, restKey: s.env["JWT_SECRET"],
		RunInfo: RunInfo{
			MySQLImage: mysqlImage, PostgreSQLImage: postgresImage,
			SessionTTLSeconds: optionDefault(options.SessionTTLSeconds, 1800),
			TenantConcurrency: optionDefault(options.MaxConcurrentQueriesPerTenant, 25),
			MultiReplica:      options.MultiReplica, TrinoIntegration: options.TrinoIntegration,
		},
	}
	for _, name := range []string{"configs", "issuer", "ca", "gateway-tls"} {
		if err := os.Mkdir(filepath.Join(directory, name), 0o755); err != nil {
			return err
		}
	}
	if err := env.prepareTLS(directory); err != nil {
		return fmt.Errorf("prepare reference TLS: %w", err)
	}
	if options.MultiReplica {
		if err := env.configureReplicaRouting(); err != nil {
			return fmt.Errorf("configure replica routing: %w", err)
		}
	}
	if err := env.writePolicy(); err != nil {
		return err
	}
	if err := s.writeEnvironment(); err != nil {
		return err
	}
	if _, err := s.compose(ctx, "config", "--quiet"); err != nil {
		return err
	}
	dockerVersion, err := s.docker(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		return err
	}
	composeVersion, err := s.docker(ctx, "compose", "version", "--short")
	if err != nil {
		return err
	}
	env.RunInfo.DockerServerVersion = dockerVersion
	env.RunInfo.ComposeVersion = composeVersion
	env.RunInfo.SourceRevision, env.RunInfo.WorkingTreeDirty = checkoutState(ctx, root)
	stackReady = true
	fmt.Fprintf(out, "Reference project %s on %s/%s, Docker %s, Compose %s\n",
		s.project, runtime.GOOS, runtime.GOARCH, dockerVersion, composeVersion)
	fmt.Fprintf(out, "Checkout: %s\nMySQL (metadata and source): %s\nPostgreSQL: %s\n", checkout(ctx, root), mysqlImage, postgresImage)
	if options.TrinoIntegration {
		fmt.Fprintf(out, "Trino coordinator: %s\n", trinoImage)
	}
	fmt.Fprintf(out, "Prepared local configuration and verified Docker in %s\n", elapsed(start))
	stage := time.Now()
	if ownedImage {
		fmt.Fprintln(out, "Building the gateway image...")
		if _, err := s.compose(ctx, "build", "data-fabric-api"); err != nil {
			return err
		}
	}
	imageID, err := s.docker(ctx, "image", "inspect", "--format", "{{.Id}}", s.image)
	if err != nil {
		return err
	}
	env.RunInfo.GatewayImage = s.image
	env.RunInfo.GatewayImageID = imageID
	fmt.Fprintf(out, "Gateway image %s (%s), prepared in %s\n", s.image, imageID, elapsed(stage))

	stage = time.Now()
	fmt.Fprintln(out, "Starting the source databases and the HTTP API...")
	services := []string{"data-fabric-api"}
	if options.TrinoIntegration {
		services = append(services, "trino")
	}
	if err := s.up(ctx, services...); err != nil {
		return err
	}
	env.APIURL, err = s.url(ctx, "data-fabric-api", "8080", "http")
	if err != nil {
		return err
	}
	if options.TrinoIntegration {
		env.TrinoCoordinatorURL, err = s.url(ctx, "trino", "8080", "http")
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "Databases and API ready in %s\n", elapsed(stage))
	if err := env.mintIdentities(); err != nil {
		return fmt.Errorf("mint reference identities: %w", err)
	}
	stage = time.Now()
	if err := env.registerSources(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "Registered %d sources with app-a in %s\n", len(env.Sources), elapsed(stage))

	stage = time.Now()
	if err := env.writePolicy(); err != nil {
		return err
	}
	// Table/column policy is loaded at startup and uses the UUIDs just
	// assigned by the API. Restart only the API before starting remote MCP.
	if _, err := s.compose(ctx, "restart", "data-fabric-api"); err != nil {
		return err
	}
	if options.MultiReplica {
		if err := s.up(ctx, "data-fabric-api-b"); err != nil {
			return err
		}
	}
	if err := s.up(ctx, "data-fabric-mcp"); err != nil {
		return err
	}
	if options.MultiReplica {
		if err := s.up(ctx, "data-fabric-mcp-b"); err != nil {
			return err
		}
		if err := s.up(ctx, "replica-ingress"); err != nil {
			return err
		}
		env.APIURL, err = s.url(ctx, "replica-ingress", "8082", "http")
		if err != nil {
			return err
		}
		env.MCPURL = env.APIURL + "/mcp"
		for _, owner := range []struct{ slot, service, port, scheme string }{
			{"rest-a", "data-fabric-api", "8080", "http"},
			{"rest-b", "data-fabric-api-b", "8080", "http"},
			{"mcp-a", "data-fabric-mcp", "8081", "https"},
			{"mcp-b", "data-fabric-mcp-b", "8081", "https"},
		} {
			ownerURL, urlErr := s.url(ctx, owner.service, owner.port, owner.scheme)
			if urlErr != nil {
				return fmt.Errorf("locate loopback test endpoint for %s: %w", owner.slot, urlErr)
			}
			env.OwnerURLs[owner.slot] = ownerURL
		}
	} else {
		env.APIURL, err = s.url(ctx, "data-fabric-api", "8080", "http")
		if err != nil {
			return err
		}
		mcpBase, err := s.url(ctx, "data-fabric-mcp", "8081", "https")
		if err != nil {
			return err
		}
		env.MCPURL = mcpBase + "/mcp"
	}
	fmt.Fprintf(out, "Policy loaded and remote MCP ready in %s\n", elapsed(stage))

	stage = time.Now()
	if err := env.verifyReuse(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "app-b reused %d sources via REST and MCP in %s\n", len(env.Sources), elapsed(stage))
	if err := env.verifyDenials(ctx); err != nil {
		return err
	}
	fmt.Fprintf(out, "Cross-tenant access, a restricted column, and invalid MCP audience rejected. Setup and smoke: %s\n", elapsed(start))
	if err := s.recordResources(ctx, out); err != nil {
		return err
	}
	if check != nil {
		return check(ctx, env)
	}
	return nil
}

func optionDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func newStack(root, directory string, options Options) *composeStack {
	project := "df-reference-" + randomHex(6)
	image := options.Image
	if image == "" {
		image = project + ":local"
	}
	ttl := options.SessionTTLSeconds
	if ttl == 0 {
		ttl = 1800
	}
	concurrent := options.MaxConcurrentQueriesPerTenant
	if concurrent == 0 {
		concurrent = 25
	}
	env := map[string]string{
		"DF_REFERENCE_DIRECTORY":               filepath.ToSlash(directory),
		"DF_REFERENCE_IMAGE":                   image,
		"DF_REFERENCE_MYSQL_IMAGE":             mysqlImage,
		"DF_REFERENCE_POSTGRES_IMAGE":          postgresImage,
		"DF_REFERENCE_TRINO_IMAGE":             trinoImage,
		"DF_REFERENCE_CONCURRENT_PER_TENANT":   strconv.Itoa(concurrent),
		"DF_REFERENCE_SESSION_TTL_SECONDS":     strconv.Itoa(ttl),
		"DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID": "reference",
		"MCP_OIDC_ISSUER":                      issuerURL,
		"MCP_OIDC_AUDIENCE":                    audience,
		"MCP_OIDC_REQUIRED_SCOPES":             queryScope,
		"MCP_OIDC_SCOPE_CLAIM":                 "scope",
		"MCP_OIDC_TENANT_CLAIM":                "tenant",
		"MCP_OIDC_ROLES_CLAIM":                 "roles",
		"MCP_OIDC_USERNAME_CLAIM":              "preferred_username",
		"MCP_OIDC_JWKS_REFRESH_INTERVAL":       "5m",
		"API_PORT":                             "8080",
		"MCP_HTTPS_PORT":                       "8443",
	}
	var secrets []string
	if options.MultiReplica {
		deployment := "reference-" + randomHex(6)
		routingKey := base64.RawURLEncoding.EncodeToString(randomBytes(32))
		nextRoutingKey := base64.RawURLEncoding.EncodeToString(randomBytes(32))
		env["ROUTING_ENABLED"] = "true"
		env["ROUTING_DEPLOYMENT_ID"] = deployment
		env["ROUTING_POLICY_REVISION"] = "reference-1"
		env["DATA_FABRIC_ROUTING_ACTIVE_KEY_ID"] = "reference"
		env["DATA_FABRIC_ROUTING_ACTIVE_KEY_ID_B"] = "reference-next"
		env["DATA_FABRIC_ROUTING_KEYS"] = fmt.Sprintf("{\"reference\":%q,\"reference-next\":%q}", routingKey, nextRoutingKey)
		secrets = append(secrets, routingKey, "[redacted]", nextRoutingKey, "[redacted]", env["DATA_FABRIC_ROUTING_KEYS"], "[redacted]")
	}
	for _, key := range []string{
		"JWT_SECRET", "METADATA_DB_ROOT_PASSWORD", "METADATA_DB_PASSWORD",
		"SAMPLE_SOURCE_ROOT_PASSWORD", "SAMPLE_READER_PASSWORD",
		"DF_REFERENCE_POSTGRES_PASSWORD", "DF_REFERENCE_POSTGRES_READER_PASSWORD",
	} {
		env[key] = randomHex(32)
		secrets = append(secrets, env[key], "[redacted]")
	}
	vaultKey := base64.StdEncoding.EncodeToString(randomBytes(32))
	env["DATA_FABRIC_CREDENTIAL_KEYS"] = `{"reference":"` + vaultKey + `"}`
	secrets = append(secrets, vaultKey, "[redacted]")
	return &composeStack{
		root: root, directory: directory, project: project, image: image,
		env: env, redact: strings.NewReplacer(secrets...), multiReplica: options.MultiReplica,
		trinoIntegration: options.TrinoIntegration,
	}
}

func (env *Environment) writePolicy() error {
	policy := env.tableAuthorizationPolicy()
	if len(policy.DataSources) == 0 {
		// The API starts once to register sources. Do not persist an enabled
		// empty policy head before their UUID-based grants are available.
		policy.Enabled = false
	}
	cfg := map[string]any{"table_authorization": policy}
	if env.replica != nil {
		cfg["routing"] = map[string]any{
			"enabled": true, "deployment_id": env.replica.deployment, "owner_slot": "", "policy_revision": "reference-1",
			"drain_timeout_seconds": ReferenceOwnerDrainTimeoutSeconds,
		}
		cfg["mcp"] = map[string]any{
			"routed_session_ttl_seconds": ReferenceRoutedMCPSessionTTLSeconds,
			"max_routed_sessions":        ReferenceMaxRoutedMCPSessions,
		}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// JSON is also valid YAML. The containing directory is mounted rather
	// than the file, so both services see the bootstrap policy at startup.
	return os.WriteFile(filepath.Join(env.stack.directory, "configs", "config.yaml"), data, 0o644)
}

func repositoryRoot(path string) (string, error) {
	var err error
	if path == "" {
		path, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(path, "deploy", "mcp", "docker-compose.reference.yml")); err == nil {
			return path, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf("could not find the Data Fabric repository")
		}
		path = parent
	}
}

func checkout(ctx context.Context, root string) string {
	revision, dirty := checkoutState(ctx, root)
	if revision == "" {
		return "unavailable"
	}
	if dirty {
		return revision + " (working tree changes)"
	}
	return revision
}

func checkoutState(ctx context.Context, root string) (string, bool) {
	command := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	command.Dir = root
	revision, err := command.Output()
	if err != nil {
		return "", false
	}
	command = exec.CommandContext(ctx, "git", "status", "--porcelain")
	command.Dir = root
	status, err := command.Output()
	return strings.TrimSpace(string(revision)), err == nil && len(status) != 0
}

func elapsed(since time.Time) time.Duration {
	return time.Since(since).Round(time.Millisecond)
}

func randomHex(size int) string {
	return hex.EncodeToString(randomBytes(size))
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	_, _ = rand.Read(data) // crypto/rand.Read either succeeds or terminates the process.
	return data
}
