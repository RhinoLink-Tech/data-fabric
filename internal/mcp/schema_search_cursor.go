package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// A cursor is bound to the caller, source, search parameters and the current
// permitted table inventory. Its MAC does not reveal the inventory hash.
// It is deliberately process-local and expires; it is not a snapshot.
type schemaSearchCursor struct {
	Offset  int    `json:"offset"`
	Expires int64  `json:"expires"`
	MAC     string `json:"mac"`
}

type schemaSearchCursorBinding struct {
	Offset    int
	Expires   int64
	Source    string
	Query     string
	Schema    string
	Limit     int
	User      string
	Tenant    string
	Strict    bool
	Inventory string
}

func schemaSearchInventory(candidates []schemaSearchCandidate) string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	for _, c := range candidates {
		_ = enc.Encode([]string{c.reference.Catalog, c.reference.Schema, c.reference.Table, c.identity.Type})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func schemaCursorMAC(key []byte, b schemaSearchCursorBinding) string {
	raw, _ := json.Marshal(b)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *MCPServer) encodeSchemaCursor(offset int, source, query, schema string, limit int, caller *security.Caller, inventory string) (string, error) {
	if len(m.schemaCursorKey) == 0 {
		return "", errors.New("schema search continuation is unavailable")
	}
	expiry := time.Now().Add(15 * time.Minute).Unix()
	b := schemaSearchCursorBinding{Offset: offset, Expires: expiry, Source: source, Query: query, Schema: schema, Limit: limit, User: caller.UserID, Tenant: caller.TenantID, Strict: caller.StrictTenantIsolation, Inventory: inventory}
	raw, err := json.Marshal(schemaSearchCursor{Offset: offset, Expires: expiry, MAC: schemaCursorMAC(m.schemaCursorKey, b)})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (m *MCPServer) decodeSchemaCursor(token, source, query, schema string, limit int, caller *security.Caller, inventory string, size int) (int, error) {
	invalid := errors.New("schema search continuation is invalid or expired; restart the search")
	if len(m.schemaCursorKey) == 0 || len(token) > 1024 {
		return 0, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, invalid
	}
	var cursor schemaSearchCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Offset < 1 || cursor.Offset >= size || cursor.Expires <= time.Now().Unix() {
		return 0, invalid
	}
	b := schemaSearchCursorBinding{Offset: cursor.Offset, Expires: cursor.Expires, Source: source, Query: query, Schema: schema, Limit: limit, User: caller.UserID, Tenant: caller.TenantID, Strict: caller.StrictTenantIsolation, Inventory: inventory}
	expected := schemaCursorMAC(m.schemaCursorKey, b)
	if !hmac.Equal([]byte(cursor.MAC), []byte(expected)) {
		return 0, invalid
	}
	return cursor.Offset, nil
}
