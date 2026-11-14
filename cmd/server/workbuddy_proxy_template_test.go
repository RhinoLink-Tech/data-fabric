package main

import (
	"os"
	"strings"
	"testing"
)

func TestWorkBuddyIdentityProxyContract(t *testing.T) {
	template, err := os.ReadFile("../../deploy/mcp/workbuddy-identity-proxy.conf.template")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../scripts/fixtures/workbuddy-identity-proxy.conf")
	if err != nil {
		t.Fatal(err)
	}
	text := string(template)
	if !strings.Contains(text, "server_name ${DF_PUBLIC_HOST};") {
		t.Fatal("deployment template must use the configured public hostname")
	}
	shared := []string{
		"location = /identity/realms/data-fabric/protocol/openid-connect/logout/logout-confirm",
		"location = /identity/realms/data-fabric/protocol/openid-connect/registrations",
		"location ^~ /identity/realms/data-fabric/account/",
		"location ^~ /identity/realms/data-fabric/protocol/openid-connect/login-status-iframe.html",
		"location ^~ /identity/realms/data-fabric/protocol/openid-connect/3p-cookies/",
		"location ^~ /identity/realms/data-fabric/login-actions/",
	}
	for _, route := range shared {
		if !strings.Contains(text, route) || !strings.Contains(string(fixture), route) {
			t.Errorf("template and test fixture must expose %q", route)
		}
	}
	location := func(path string) string {
		prefix := "  location = " + path + " {"
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, prefix) {
				return line
			}
		}
		t.Errorf("missing location %s", path)
		return ""
	}
	account := location("/account/start")
	if !strings.Contains(account, "return 302 https://${DF_PUBLIC_HOST}/identity/realms/data-fabric/account/;") {
		t.Error("account switch must redirect to the external HTTPS origin")
	}
	registration := location("/register/start")
	if !strings.Contains(registration, "return 302 "+string(rune(39))+"https://${DF_PUBLIC_HOST}/identity/") ||
		!strings.Contains(registration, "code_challenge=${DF_SIGNUP_CHALLENGE}") ||
		!strings.Contains(registration, "code_challenge_method=S256") {
		t.Error("registration must use external HTTPS and the configured PKCE challenge")
	}
	for _, line := range []string{account, registration} {
		for _, cookie := range []string{"AUTH_SESSION_ID=;", "KC_AUTH_SESSION_HASH=;", "KC_RESTART=;"} {
			if !strings.Contains(line, cookie) {
				t.Errorf("switch must clear stale %s", cookie)
			}
		}
		if !strings.Contains(line, "Path=/identity/realms/data-fabric/; Max-Age=0; Secure; HttpOnly; SameSite=None") {
			t.Error("stale auth cookies must be cleared at the identity realm path")
		}
	}
	if !strings.Contains(text, "location / { return 404; }") {
		t.Error("proxy must deny unlisted routes")
	}
}
