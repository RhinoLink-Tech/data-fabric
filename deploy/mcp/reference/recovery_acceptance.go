package reference

// Recovery acceptance exercises the documented single-host Compose profile in
// disposable GitHub Actions projects. The only overlay provides a synthetic
// OIDC issuer and its CA; recovery.py itself uses the standard Compose file.
import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const recoveryOverlay = `
services:
  oidc-fixture:
    image: ${DATA_FABRIC_IMAGE}
    entrypoint: ["python3", "/reference/oidc_fixture.py"]
    volumes:
      - ./oidc_fixture.py:/reference/oidc_fixture.py:ro
      - ${DF_RECOVERY_FIXTURE_DIR}/issuer:/reference/issuer:ro
    healthcheck:
      test: ["CMD", "nc", "-z", "127.0.0.1", "8443"]
      interval: 2s
      timeout: 2s
      retries: 30
  data-fabric-mcp:
    depends_on:
      oidc-fixture:
        condition: service_healthy
    environment:
      SSL_CERT_DIR: /etc/ssl/certs:/etc/data-fabric/reference-ca
    volumes:
      - ${DF_RECOVERY_FIXTURE_DIR}/ca:/etc/data-fabric/reference-ca:ro
`

// RecoveryAcceptance uses fresh checkouts, an exact-revision local image,
// generated test identities, age and newly named Compose projects. It cannot
// operate on a local user's existing deployment.
func RecoveryAcceptance(ctx context.Context, repo string, out io.Writer) error {
	return recoveryAcceptance(ctx, repo, "", PublishedRelease{}, out)
}

// PublishedRelease binds a public source archive to the matching immutable
// image. Only release tags listed by PublishedReleaseByTag are accepted.
type PublishedRelease struct {
	Tag                         string
	ArchiveSHA                  string
	SourceSHA                   string
	Image                       string
	LegacySourceCredentialAudit bool
}

var publishedReleases = map[string]PublishedRelease{
	"v2.1.0-preview.2": {
		Tag:                         "v2.1.0-preview.2",
		ArchiveSHA:                  "111cb47df04b1b30abbf714e8bcf71660441aa9ad80426849a1138efe3b7be75",
		SourceSHA:                   "281260f6c2000419222e55a99e984b8dd5094ab8",
		Image:                       "ghcr.io/rhinolink-tech/data-fabric:v2.1.0-preview.2@sha256:c3e6b06848931b84610bcb399d3eec126a5a4f2317e4238f27b7fdc458e8520e",
		LegacySourceCredentialAudit: true,
	},
	"v2.1.0-preview.3": {
		Tag:        "v2.1.0-preview.3",
		ArchiveSHA: "ebb4a0ef456447f90ae3b1f1d3fe3d2719785974fc82a746a2e2278dae8da0c1",
		SourceSHA:  "2b2a218b209edb7d67ef02b4762d195f0622cd3c",
		Image:      "ghcr.io/rhinolink-tech/data-fabric:v2.1.0-preview.3@sha256:0c5ada5c16f9a411ecc80f8fd1635733ab7e88cb5f2bdc2d74f27b54ca6278af",
	},
}

// PublishedReleaseByTag returns a reviewable, immutable release identity. It
// intentionally rejects unlisted tags instead of accepting a floating or
// caller-provided image reference.
func PublishedReleaseByTag(tag string) (PublishedRelease, error) {
	release, ok := publishedReleases[tag]
	if !ok {
		return PublishedRelease{}, fmt.Errorf("unsupported published release %q", tag)
	}
	return release, nil
}

// PublishedRecoveryAcceptance uses a selected immutable published image and
// source archive, with the current recovery helper kept outside the extracted
// tree.
func PublishedRecoveryAcceptance(ctx context.Context, repo, releaseArchive string, release PublishedRelease, out io.Writer) error {
	if releaseArchive == "" {
		return fmt.Errorf("published source archive is required")
	}
	if verified, err := PublishedReleaseByTag(release.Tag); err != nil || verified != release {
		return fmt.Errorf("published release identity is not allowlisted")
	}
	return recoveryAcceptance(ctx, repo, releaseArchive, release, out)
}

func recoveryAcceptance(ctx context.Context, repo, releaseArchive string, release PublishedRelease, out io.Writer) (err error) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return fmt.Errorf("recovery acceptance only runs on an isolated GitHub Actions runner")
	}
	repo, err = repositoryRoot(repo)
	if err != nil {
		return err
	}
	revision, err := recoveryCommand(ctx, repo, nil, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	revision = strings.TrimSpace(revision)
	if len(revision) != 40 || revision != os.Getenv("GITHUB_SHA") {
		return fmt.Errorf("checkout must match the exact GitHub Actions candidate SHA")
	}
	clean, err := recoveryCommand(ctx, repo, nil, nil, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil || strings.TrimSpace(clean) != "" {
		return fmt.Errorf("candidate checkout must be clean")
	}
	temp, err := os.MkdirTemp("", "df-recovery-acceptance-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(temp)) }()
	fixture := filepath.Join(temp, "fixture")
	for _, part := range []string{"ca", "issuer", "gateway-tls"} {
		if err := os.MkdirAll(filepath.Join(fixture, part), 0755); err != nil {
			return err
		}
	}
	env := &Environment{Identities: make(map[string]Identity)}
	if err := env.prepareTLS(fixture); err != nil {
		return err
	}
	overlay := filepath.Join(temp, "oidc-overlay.yml")
	if err := os.WriteFile(overlay, []byte(recoveryOverlay), 0600); err != nil {
		return err
	}
	source := filepath.Join(temp, "source")
	target := filepath.Join(temp, "restored")
	artifactRevision := revision
	configRevision := revision
	if releaseArchive != "" {
		releaseArchive, err = filepath.Abs(releaseArchive)
		if err != nil {
			return err
		}
		configRevision = "unknown"
	}
	if releaseArchive == "" {
		if err := recoveryClone(ctx, repo, source, revision); err != nil {
			return err
		}
		if err := recoveryClone(ctx, repo, target, revision); err != nil {
			return err
		}
	} else {
		artifactRevision = release.SourceSHA
		if err := recoveryExtractPublished(ctx, releaseArchive, source, release); err != nil {
			return err
		}
		if err := recoveryExtractPublished(ctx, releaseArchive, target, release); err != nil {
			return err
		}
	}
	tag := "df-recovery-" + randomHex(6)
	originalProject, restoredProject := tag+"-source", tag+"-restore"
	image := tag + ":candidate"
	if releaseArchive != "" {
		image = release.Image
	}
	imageBuilt := false
	vars := map[string]string{"DF_RECOVERY_FIXTURE_DIR": fixture}
	compose := func(root, project string, args ...string) (string, error) {
		deploy := filepath.Join(root, "deploy", "mcp")
		command := []string{"compose", "--project-name", project, "--env-file", filepath.Join(deploy, ".env"), "-f", filepath.Join(deploy, "docker-compose.yml")}
		if releaseArchive != "" {
			command = append(command, "-f", filepath.Join(deploy, "docker-compose.release.yml"))
		}
		command = append(command, "-f", filepath.Join(deploy, "docker-compose.recovery.yml"), "-f", overlay)
		return recoveryCommand(ctx, deploy, vars, nil, "docker", append(command, args...)...)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, item := range []struct{ root, project string }{{source, originalProject}, {target, restoredProject}} {
			deploy := filepath.Join(item.root, "deploy", "mcp")
			if _, statErr := os.Stat(filepath.Join(deploy, ".env")); statErr == nil {
				args := []string{"compose", "--project-name", item.project, "--env-file", filepath.Join(deploy, ".env"), "-f", filepath.Join(deploy, "docker-compose.yml")}
				if releaseArchive != "" {
					args = append(args, "-f", filepath.Join(deploy, "docker-compose.release.yml"))
				}
				args = append(args, "-f", filepath.Join(deploy, "docker-compose.recovery.yml"), "-f", overlay, "down", "--volumes", "--remove-orphans")
				_, cleanupErr := recoveryCommand(cleanupCtx, deploy, vars, nil, "docker", args...)
				if cleanupErr != nil {
					err = errors.Join(err, fmt.Errorf("cleanup of isolated recovery project failed"))
				}
			}
		}
		if imageBuilt {
			if _, cleanupErr := recoveryCommand(cleanupCtx, repo, nil, nil, "docker", "image", "rm", image); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("cleanup of isolated candidate image failed"))
			}
		}
	}()
	fmt.Fprintln(out, "Preparing clean checkouts and the isolated synthetic issuer")
	deploy := filepath.Join(source, "deploy", "mcp")
	if _, err := recoveryCommand(ctx, source, nil, nil, "python3", "scripts/mcp/onboard.py", "init", "--directory", deploy); err != nil {
		return err
	}
	if err := recoveryConfigure(filepath.Join(deploy, ".env"), map[string]string{
		"DATA_FABRIC_IMAGE": image, "DATA_FABRIC_SOURCE_REVISION": configRevision,
		"MCP_OIDC_ISSUER": issuerURL, "MCP_OIDC_AUDIENCE": audience,
		"API_PORT": "18080", "MCP_HTTPS_PORT": "18443",
	}); err != nil {
		return err
	}
	if releaseArchive == "" {
		fmt.Fprintln(out, "Building exact-revision candidate image")
		if _, err := recoveryCommand(ctx, source, nil, nil, "docker", "build", "--quiet", "--tag", image, "--build-arg", "DATA_FABRIC_VERSION=ci", "--build-arg", "DATA_FABRIC_SOURCE_REVISION="+revision, "."); err != nil {
			return fmt.Errorf("build candidate: %w", err)
		}
	} else {
		fmt.Fprintf(out, "Pulling published %s image by immutable digest\n", release.Tag)
		if _, err := recoveryCommand(ctx, source, nil, nil, "docker", "pull", image); err != nil {
			return fmt.Errorf("pull published image: %w", err)
		}
	}
	imageBuilt = true
	fmt.Fprintln(out, "Pulling the documented synthetic MySQL image on the CI runner")
	if _, err := compose(source, originalProject, "pull", "metadata-db", "sample-source"); err != nil {
		return fmt.Errorf("pull synthetic database image: %w", err)
	}
	fmt.Fprintln(out, "Starting documented profile with isolated synthetic issuer")
	if _, err := compose(source, originalProject, "up", "-d", "--wait", "--wait-timeout", "240", "--no-build", "--pull", "never"); err != nil {
		return err
	}
	values, err := recoveryEnv(filepath.Join(deploy, ".env"))
	if err != nil {
		return err
	}
	env.restKey = values["JWT_SECRET"]
	if err := env.mintIdentities(); err != nil {
		return err
	}
	if err := recoverySetEndpoint(env, deploy, 18080, 18443); err != nil {
		return err
	}
	var created sourceRecord
	if err := env.rest(ctx, "app-a", http.MethodPost, "/api/v1/datasources", map[string]any{
		"name": "recovery-synthetic-mysql", "type": "mysql", "config": map[string]any{
			"host": "sample-source", "port": 3306, "database": "sample", "username": "sample_reader", "password": values["SAMPLE_READER_PASSWORD"],
		},
	}, http.StatusCreated, &created); err != nil {
		return fmt.Errorf("register source: %w", err)
	}
	if created.ID == "" {
		return fmt.Errorf("empty registered source ID")
	}
	if err := recoveryCheckAccess(ctx, env, created.ID); err != nil {
		return fmt.Errorf("before backup: %w", err)
	}
	fmt.Fprintln(out, "PASS original documented profile: authorized read and cross-tenant denial")

	staging, err := os.MkdirTemp("/dev/shm", "df-recovery-staging-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	identity := filepath.Join(temp, "age.key")
	if _, err := recoveryCommand(ctx, temp, nil, nil, "age-keygen", "-o", identity); err != nil {
		return err
	}
	recipient, err := recoveryCommand(ctx, temp, nil, nil, "age-keygen", "-y", identity)
	if err != nil {
		return err
	}
	archive := filepath.Join(temp, "profile.age")
	helper := filepath.Join(repo, "scripts", "mcp", "recovery.py")
	var sourceArchiveArgs []string
	if releaseArchive != "" && release.LegacySourceCredentialAudit {
		sourceArchiveArgs = []string{"--release-source-archive", releaseArchive}
	}
	fmt.Fprintln(out, "Creating encrypted metadata and configuration backup")
	backupArgs := []string{helper, "backup", "--directory", deploy, "--project-name", originalProject, "--recipient", strings.TrimSpace(recipient), "--staging-dir", staging, "--output", archive}
	backupArgs = append(backupArgs, sourceArchiveArgs...)
	backupOutput, err := recoveryCommand(ctx, source, nil, nil, "python3", backupArgs...)
	if err != nil {
		return fmt.Errorf("backup preflight or snapshot: %w", err)
	}
	if releaseArchive != "" {
		if !strings.Contains(backupOutput, "Legacy image does not support source credential audit") {
			return fmt.Errorf("expected legacy source-credential audit warning")
		}
		runningValues, err := recoveryEnv(filepath.Join(deploy, ".env"))
		if err != nil || runningValues["DATA_FABRIC_SOURCE_REVISION"] != "unknown" {
			return fmt.Errorf("backup changed the running archive deployment revision")
		}
		fmt.Fprintln(out, "PASS legacy image warning and unchanged running archive configuration")
	}
	verifyArgs := []string{helper, "verify", "--archive", archive, "--identity-file", identity, "--staging-dir", staging}
	if _, err := recoveryCommand(ctx, source, nil, nil, "python3", verifyArgs...); err != nil {
		return err
	}
	wrong := filepath.Join(temp, "wrong.key")
	if _, err := recoveryCommand(ctx, temp, nil, nil, "age-keygen", "-o", wrong); err != nil {
		return err
	}
	if _, err := recoveryCommand(ctx, source, nil, nil, "python3", helper, "verify", "--archive", archive, "--identity-file", wrong, "--staging-dir", staging); err == nil {
		return fmt.Errorf("wrong age identity unexpectedly verified")
	}
	damaged, err := os.ReadFile(archive)
	if err != nil {
		return err
	}
	if len(damaged) == 0 {
		return fmt.Errorf("empty encrypted archive")
	}
	damaged[len(damaged)-1] ^= 1
	damagedPath := filepath.Join(temp, "damaged.age")
	if err := os.WriteFile(damagedPath, damaged, 0600); err != nil {
		return err
	}
	if _, err := recoveryCommand(ctx, source, nil, nil, "python3", helper, "verify", "--archive", damagedPath, "--identity-file", identity, "--staging-dir", staging); err == nil {
		return fmt.Errorf("damaged archive unexpectedly verified")
	}
	if err := recoveryWaitReady(ctx, env); err != nil {
		return err
	}
	if err := recoveryCheckAccess(ctx, env, created.ID); err != nil {
		return fmt.Errorf("after backup restart: %w", err)
	}
	fmt.Fprintln(out, "PASS encrypted backup, corruption and wrong-key rejection, original profile restart")

	fmt.Fprintln(out, "Preparing fresh checkout and metadata volume")
	targetDeploy := filepath.Join(target, "deploy", "mcp")
	restoreArgs := []string{helper, "restore", "--archive", archive, "--identity-file", identity, "--staging-dir", staging, "--target-deploy-dir", targetDeploy, "--project-name", restoredProject, "--api-port", "28080", "--mcp-port", "28443"}
	restoreArgs = append(restoreArgs, sourceArchiveArgs...)
	if _, err := recoveryCommand(ctx, source, nil, nil, "python3", restoreArgs...); err != nil {
		return fmt.Errorf("restore preparation: %w", err)
	}
	if releaseArchive != "" {
		restoredValues, err := recoveryEnv(filepath.Join(targetDeploy, ".env"))
		if err != nil || restoredValues["DATA_FABRIC_SOURCE_REVISION"] != release.SourceSHA || restoredValues["DATA_FABRIC_IMAGE"] != release.Image {
			return fmt.Errorf("restored archive configuration did not retain verified release identity")
		}
	}
	if _, err := recoveryCommand(ctx, target, nil, nil, "python3", "scripts/mcp/onboard.py", "init", "--directory", targetDeploy); err != nil {
		return err
	}
	if _, err := compose(target, restoredProject, "up", "-d", "--wait", "--wait-timeout", "240", "--no-build", "--pull", "never", "metadata-db"); err != nil {
		return err
	}
	sql, err := os.Open(filepath.Join(targetDeploy, "recovery", "metadata.sql"))
	if err != nil {
		return err
	}
	importArgs := []string{"compose", "--project-name", restoredProject, "--env-file", filepath.Join(targetDeploy, ".env"), "-f", filepath.Join(targetDeploy, "docker-compose.yml")}
	if releaseArchive != "" {
		importArgs = append(importArgs, "-f", filepath.Join(targetDeploy, "docker-compose.release.yml"))
	}
	importArgs = append(importArgs, "-f", filepath.Join(targetDeploy, "docker-compose.recovery.yml"), "-f", overlay, "exec", "-T", "metadata-db", "sh", "-ec", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root`)
	_, importErr := recoveryCommand(ctx, targetDeploy, vars, sql, "docker", importArgs...)
	_ = sql.Close()
	if importErr != nil {
		return importErr
	}
	if _, err := compose(target, restoredProject, "up", "-d", "--wait", "--wait-timeout", "240", "--no-build", "--pull", "never"); err != nil {
		return err
	}
	if err := recoverySetEndpoint(env, targetDeploy, 28080, 28443); err != nil {
		return err
	}
	if err := recoveryCheckAccess(ctx, env, created.ID); err != nil {
		return fmt.Errorf("after restore: %w", err)
	}
	fmt.Fprintf(out, "PASS fresh-volume restore: authorized read and cross-tenant denial; source=%s; helper=%s; image=%s\n", artifactRevision, revision, image)
	return nil
}

func recoveryExtractPublished(ctx context.Context, archive, target string, release PublishedRelease) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("could not read %s archive", release.Tag)
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != release.ArchiveSHA {
		return fmt.Errorf("%s source archive checksum mismatch", release.Tag)
	}
	if err := os.Mkdir(target, 0700); err != nil {
		return err
	}
	if _, err := recoveryCommand(ctx, target, nil, nil, "tar", "-xzf", archive, "--strip-components=1", "-C", target); err != nil {
		return fmt.Errorf("extract verified %s archive: %w", release.Tag, err)
	}
	return nil
}

func recoveryClone(ctx context.Context, root, target, revision string) error {
	if _, err := recoveryCommand(ctx, root, nil, nil, "git", "init", "--quiet", target); err != nil {
		return err
	}
	if _, err := recoveryCommand(ctx, target, nil, nil, "git", "fetch", "--quiet", "--depth=1", root, revision); err != nil {
		return err
	}
	_, err := recoveryCommand(ctx, target, nil, nil, "git", "checkout", "--quiet", "--detach", "FETCH_HEAD")
	return err
}

func recoveryEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = v
		}
	}
	return values, scanner.Err()
}

func recoveryConfigure(path string, values map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	lines := strings.Split(string(b), "\n")
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if v, found := values[key]; ok && found {
			lines[i] = key + "=" + v
			seen[key] = true
		}
	}
	for k, v := range values {
		if !seen[k] {
			lines = append(lines, k+"="+v)
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

func recoverySetEndpoint(env *Environment, deploy string, apiPort, mcpPort int) error {
	cert, err := os.ReadFile(filepath.Join(deploy, "tls", "tls.crt"))
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		return fmt.Errorf("invalid synthetic gateway certificate")
	}
	env.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	env.APIURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	env.MCPURL = fmt.Sprintf("https://127.0.0.1:%d/mcp", mcpPort)
	return nil
}

func recoveryCheckAccess(ctx context.Context, env *Environment, id string) error {
	owner, err := env.openMCP(ctx, "app-a")
	if err != nil {
		return err
	}
	defer owner.Close()
	var result struct {
		Entries [][]json.RawMessage `json:"entries"`
	}
	if err := mcpCall(ctx, owner, "execute_sql_query", map[string]any{"datasource_id": id, "sql": "SELECT id, amount FROM sample.orders WHERE id = 1", "batch_size": 10}, false, &result); err != nil {
		return err
	}
	if !expectedOrder(result.Entries) {
		return fmt.Errorf("unexpected synthetic order result")
	}
	outsider, err := env.openMCP(ctx, "app-c")
	if err != nil {
		return err
	}
	defer outsider.Close()
	var sources struct {
		DataSources []sourceRecord `json:"data_sources"`
	}
	if err := mcpCall(ctx, outsider, "list_data_sources", nil, false, &sources); err != nil {
		return err
	}
	for _, s := range sources.DataSources {
		if s.ID == id {
			return fmt.Errorf("cross-tenant source was visible")
		}
	}
	return mcpCall(ctx, outsider, "execute_sql_query", map[string]any{"datasource_id": id, "sql": "SELECT id FROM sample.orders"}, true, nil)
}

func recoveryWaitReady(ctx context.Context, env *Environment) error {
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.APIURL+"/health", nil)
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				req, err = http.NewRequestWithContext(ctx, http.MethodGet, env.MCPURL, nil)
				if err != nil {
					return err
				}
				resp, err = env.mcpHTTPClient().Do(req)
				if err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusUnauthorized {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("original profile did not return to healthy authenticated service after backup")
}

// Do not print stdout/stderr from subprocesses: Compose, the dump and build
// can contain generated secrets. Error messages identify only the tool.
func recoveryCommand(ctx context.Context, dir string, extra map[string]string, input io.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin = input
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "DATA_FABRIC_") || strings.HasPrefix(upper, "MCP_") ||
			strings.HasPrefix(upper, "QUERY_") || strings.HasPrefix(upper, "METADATA_DB_") ||
			strings.HasPrefix(upper, "SAMPLE_") || strings.HasPrefix(upper, "COMPOSE_") ||
			upper == "JWT_SECRET" || upper == "API_PORT" || upper == "DF_RECOVERY_FIXTURE_DIR" {
			continue
		}
		if _, ok := extra[key]; !ok {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.Output()
	if err != nil {
		// The recovery helper deliberately emits generic, redacted errors.
		// Retain its single diagnostic line without logging other subprocess
		// output, which can include generated credentials.
		if name == "python3" && len(args) > 0 && filepath.Base(args[0]) == "recovery.py" {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				for _, line := range strings.Split(string(exitErr.Stderr), "\n") {
					if strings.HasPrefix(line, "error: ") && len(line) <= 300 {
						return "", fmt.Errorf("recovery helper: %s", line)
					}
				}
			}
		}
		return "", fmt.Errorf("%s failed: %w", name, err)
	}
	return string(b), nil
}
