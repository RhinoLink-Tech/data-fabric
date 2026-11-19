package main

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

type selfServiceTestValidator struct {
	claims *security.Claims
	err    error
}

func (f selfServiceTestValidator) ValidateMCPToken(context.Context, string) (*security.Claims, error) {
	return f.claims, f.err
}

func TestSelfServiceRequiresVerifiedIssuerAndNeverModifiesExistingTenant(t *testing.T) {
	original := &security.Claims{UserID: "alice", EmailVerified: true, RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://issuer.example", Subject: "alice"}}
	calls := 0
	v := &selfServiceValidator{next: selfServiceTestValidator{claims: original}, issuer: "https://issuer.example", provision: func(_ context.Context, subject, tenant string) error {
		calls++
		if subject != "alice" || len(tenant) != 34 {
			t.Fatal("wrong provision identity")
		}
		return nil
	}}
	got, err := v.ValidateMCPToken(context.Background(), "signed")
	if err != nil || got.TenantID == "" || original.TenantID != "" || calls != 1 {
		t.Fatalf("verified new user failed: %v", err)
	}
	again, err := v.ValidateMCPToken(context.Background(), "signed")
	if err != nil || again.TenantID != got.TenantID {
		t.Fatal("tenant not stable")
	}
	testCases := []struct {
		name   string
		claims *security.Claims
		err    error
	}{
		{"unverified", &security.Claims{UserID: "alice", RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://issuer.example", Subject: "alice"}}, nil},
		{"wrong issuer", &security.Claims{UserID: "alice", EmailVerified: true, RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://other.example", Subject: "alice"}}, nil},
		{"mismatched subject", &security.Claims{UserID: "alice", EmailVerified: true, RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://issuer.example", Subject: "bob"}}, nil},
		{"invalid token", nil, errors.New("signature invalid")},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			v.next = selfServiceTestValidator{claims: tc.claims, err: tc.err}
			if _, e := v.ValidateMCPToken(context.Background(), "token"); e == nil {
				t.Fatal("untrusted account accepted")
			}
			if calls != before {
				t.Fatal("provision called for untrusted account")
			}
		})
	}
	before := calls
	existing := &security.Claims{UserID: "old", TenantID: "existing"}
	v.next = selfServiceTestValidator{claims: existing}
	got, err = v.ValidateMCPToken(context.Background(), "token")
	if err != nil || got.TenantID != "existing" || calls != before {
		t.Fatal("existing identity changed")
	}
	v.next = selfServiceTestValidator{claims: original}
	v.provision = func(context.Context, string, string) error { return errors.New("unavailable") }
	if _, err = v.ValidateMCPToken(context.Background(), "token"); err == nil {
		t.Fatal("failed provisioning accepted")
	}
}
