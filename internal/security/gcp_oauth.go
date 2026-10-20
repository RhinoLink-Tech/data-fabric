package security

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GCPAuth handles Google Cloud Platform authentication
type GCPAuth struct {
	credentialsJSON []byte
	tokenSource     oauth2.TokenSource
}

// NewGCPAuthFromJSON creates a new GCP authenticator from service account JSON
func NewGCPAuthFromJSON(credentialsJSON []byte) (*GCPAuth, error) {
	return &GCPAuth{
		credentialsJSON: credentialsJSON,
	}, nil
}

// NewGCPAuthFromFile creates a new GCP authenticator from a service account file
func NewGCPAuthFromFile(credentialsPath string) (*GCPAuth, error) {
	credentialsJSON, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}
	return NewGCPAuthFromJSON(credentialsJSON)
}

// NewGCPAuthFromADC creates a new GCP authenticator using Application Default Credentials
func NewGCPAuthFromADC(ctx context.Context) (*GCPAuth, error) {
	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/bigquery")
	if err != nil {
		return nil, fmt.Errorf("failed to create ADC token source: %w", err)
	}

	return &GCPAuth{
		tokenSource: ts,
	}, nil
}

// GetToken retrieves an OAuth2 access token
func (g *GCPAuth) GetToken(ctx context.Context) (*oauth2.Token, error) {
	if g.tokenSource != nil {
		return g.tokenSource.Token()
	}

	// Parse service account JSON and create JWT token source
	creds, err := google.CredentialsFromJSON(ctx, g.credentialsJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse credentials JSON: %w", err)
	}

	return creds.TokenSource.Token()
}

// RefreshToken refreshes an expired OAuth2 token
func (g *GCPAuth) RefreshToken(ctx context.Context) (*oauth2.Token, error) {
	return g.GetToken(ctx)
}

// ValidateConfig validates the GCP configuration
func (g *GCPAuth) ValidateConfig() error {
	if g.credentialsJSON == nil && g.tokenSource == nil {
		return fmt.Errorf("either credentials JSON or token source must be provided")
	}
	return nil
}
