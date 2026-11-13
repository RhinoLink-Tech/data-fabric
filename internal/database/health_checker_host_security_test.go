package database

import (
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"strings"
	"testing"
)

func TestRedactConnectionErrorHostCredentials(t *testing.T) {
	cases := []struct {
		host, message string
		secrets       []string
	}{
		{"postgres://testuser:secretpass@example.test/db?sig=secrettoken&custom=othersecret#secretfragment",
			"user testuser password secretpass sig secrettoken othersecret secretfragment postgres://testuser:secretpass@example.test/db?sig=secrettoken&custom=othersecret#secretfragment",
			[]string{"testuser", "secretpass", "secrettoken", "othersecret", "secretfragment"}},
		{"mysql://testuser:secret%40pass@example.test/db?sig=some%2Bvalue",
			"testuser secret@pass secret%40pass some+value some%2Bvalue",
			[]string{"testuser", "secret@pass", "secret%40pass", "some+value", "some%2Bvalue"}},
		{"invalid-user:invalid-pass@host?sig=badtoken",
			"driver error invalid-user invalid-pass badtoken",
			[]string{"invalid-user", "invalid-pass", "badtoken"}},
	}
	for _, c := range cases {
		got := redactConnectionError(errors.New(c.message), &model.DataSourceConfig{Host: c.host})
		for _, secret := range c.secrets {
			if strings.Contains(got, secret) {
				t.Errorf("error leaked %q", secret)
			}
		}
	}
}
func TestRedactConnectionErrorRetainsOrdinaryDiagnostics(t *testing.T) {
	got := redactConnectionError(errors.New("connection refused"), &model.DataSourceConfig{Host: "db.example"})
	if got != "connection refused" {
		t.Fatalf("lost ordinary diagnostic: %q", got)
	}
}

func TestRedactConnectionErrorFailsClosedForEncodedHostParts(t *testing.T) {
	cases := []struct{ host, detail string }{
		{"https://u:p@example.test/?custom=a%2fb", "a%2fb"},
		{"https://u:p@example.test/?custom=a%2Fb", "a%2Fb"},
		{"https://a%2fb:p@example.test/", "a%2fb"},
		{"https://a%2Fb:p@example.test/", "a%2Fb"},
		{"https://u:p@example.test/#a%2fb", "a%2fb"},
		{"https://u:p@example.test/#a%2Fb", "a%2Fb"},
		{"non-url-user:pass@host?sig=a%2fb", "a%2fb"},
		{"https://invalid%zz?token=a%2fb", "a%2fb"},
	}
	for _, c := range cases {
		got := redactConnectionError(errors.New("driver returned "+c.detail), &model.DataSourceConfig{Host: c.host})
		if strings.Contains(got, c.detail) || got != "connection failed; check the connection configuration" {
			t.Errorf("failed to hide encoded error fragment %q", c.detail)
		}
	}
}

func TestReadOnlyWarningHidesEncodedHostFragment(t *testing.T) {
	config := &model.DataSourceConfig{Host: "https://u:p@example.test/?custom=a%2fb"}
	got := redactReadOnlyWarning("could not inspect account privileges: a%2fb", config)
	if strings.Contains(got, "a%2fb") || !strings.Contains(got, "review account permissions") {
		t.Fatalf("unsafe or misleading warning: %q", got)
	}
}
