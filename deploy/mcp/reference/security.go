package reference

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/golang-jwt/jwt/v5"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

const (
	issuerHost = "oidc-fixture"
	issuerURL  = "https://" + issuerHost + ":8443"
	audience   = "data-fabric-mcp"
	queryScope = "data-fabric.query"
	signingKID = "reference-key"
	tokenTTL   = 30 * time.Minute
)

// Identity contains short-lived synthetic credentials for use by a check in
// the reference environment. Keep the bearer tokens in process memory.
type Identity struct {
	Subject   string
	Tenant    string
	RESTToken string `json:"-"`
	MCPToken  string `json:"-"`
}

func (env *Environment) MintREST(identityKey string, mutate func(jwt.MapClaims)) (string, error) {
	id, ok := env.Identities[identityKey]
	if !ok {
		return "", fmt.Errorf("unknown reference identity")
	}
	var roles []string
	if identityKey == "operator" {
		roles = []string{"admin"}
	}
	token, err := security.NewJWTManager(env.restKey, tokenTTL).GenerateToken(id.Subject, id.Tenant, id.Subject, roles)
	if err != nil || mutate == nil {
		return token, err
	}
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return "", fmt.Errorf("read synthetic REST claims: %w", err)
	}
	mutate(claims)
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(env.restKey))
}

func (env *Environment) MintMCP(identityKey string, mutate func(jwt.MapClaims)) (string, error) {
	id, ok := env.Identities[identityKey]
	if !ok || identityKey == "operator" {
		return "", fmt.Errorf("unknown reference MCP identity")
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": issuerURL, "sub": id.Subject, "aud": audience,
		"iat": now.Unix(), "exp": now.Add(tokenTTL).Unix(),
		"scope": queryScope, "tenant": id.Tenant, "preferred_username": id.Subject,
		"roles": []string{"reader"},
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = signingKID
	return token.SignedString(env.oidcKey)
}

func (env *Environment) mintIdentities() error {
	for _, key := range []string{"app-a", "app-b", "app-c", "operator"} {
		id := Identity{Subject: key, Tenant: "tenant-a"}
		if key == "app-c" {
			id.Tenant = "tenant-b"
		}
		env.Identities[key] = id
		var err error
		id.RESTToken, err = env.MintREST(key, nil)
		if err != nil {
			return err
		}
		if key != "operator" {
			id.MCPToken, err = env.MintMCP(key, nil)
			if err != nil {
				return err
			}
		}
		env.Identities[key] = id
	}
	return nil
}

// prepareTLS creates a separate trust root for each reference run. TLS leaf
// keys are scoped to their container mounts, and the JWT signing key stays in
// the runner. The temporary directory protects the container-readable files.
func (env *Environment) prepareTLS(directory string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	root := x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Data Fabric reference CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, &root, &root, &key.PublicKey, key)
	if err != nil {
		return err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(rootCert)
	env.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	if err := writePEM(filepath.Join(directory, "ca", "ca.crt"), "CERTIFICATE", rootDER); err != nil {
		return err
	}
	gatewayTLS := x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"localhost", "data-fabric-mcp", "data-fabric-mcp-b"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	if err := writeServerTLS(filepath.Join(directory, "gateway-tls"), &gatewayTLS, rootCert, key); err != nil {
		return err
	}
	issuerTLS := x509.Certificate{SerialNumber: big.NewInt(3), DNSNames: []string{issuerHost}}
	issuerDirectory := filepath.Join(directory, "issuer")
	if err := writeServerTLS(issuerDirectory, &issuerTLS, rootCert, key); err != nil {
		return err
	}
	env.oidcKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	discovery := map[string]string{"issuer": issuerURL, "jwks_uri": issuerURL + "/jwks"}
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &env.oidcKey.PublicKey, KeyID: signingKID, Algorithm: "RS256", Use: "sig",
	}}}
	for filename, doc := range map[string]any{"discovery.json": discovery, "jwks.json": jwks} {
		data, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(issuerDirectory, filename), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeServerTLS(directory string, leaf, root *x509.Certificate, rootKey *ecdsa.PrivateKey) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	leaf.NotBefore, leaf.NotAfter = root.NotBefore, root.NotAfter
	leaf.KeyUsage = x509.KeyUsageDigitalSignature
	leaf.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, rootKey)
	if err != nil {
		return err
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(filepath.Join(directory, "tls.crt"), "CERTIFICATE", der); err != nil {
		return err
	}
	return writePEM(filepath.Join(directory, "tls.key"), "PRIVATE KEY", privateDER)
}

func writePEM(path, kind string, data []byte) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}), 0o644)
}
