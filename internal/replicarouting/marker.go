package replicarouting

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	REST = "rest"
	MCP  = "mcp"

	maxMarkerLength = 512
)

var (
	errInvalidMarker = errors.New("invalid or expired routing marker")
	markerEncoding   = base64.RawURLEncoding.Strict()
)

// KeyRing verifies routing markers using operator-configured keys.
type KeyRing struct {
	deployment string
	keys       map[string][]byte
}

// ParseKeyRing parses a JSON object mapping key IDs to unpadded base64url keys.
func ParseKeyRing(deployment, config string) (*KeyRing, error) {
	if !validLabel(deployment, 64) {
		return nil, errors.New("routing deployment ID must contain 1–64 lowercase letters, digits or hyphens")
	}
	var encoded map[string]string
	if len(config) > 8192 || json.Unmarshal([]byte(config), &encoded) != nil || len(encoded) == 0 || len(encoded) > 8 {
		return nil, errors.New("DATA_FABRIC_ROUTING_KEYS must be a JSON object containing 1–8 keys")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		key, err := markerEncoding.DecodeString(value)
		if !validLabel(id, 32) || err != nil || len(key) < 32 || len(key) > 64 || markerEncoding.EncodeToString(key) != value {
			return nil, errors.New("routing keys require a lowercase key ID and an unpadded base64url key of 32–64 bytes")
		}
		keys[id] = key
	}
	return &KeyRing{deployment: deployment, keys: keys}, nil
}

// Owner issues markers for one process generation and transport.
type Owner struct {
	ring *KeyRing
	key  string
	pool string
	slot string
	boot string
}

func (r *KeyRing) NewOwner(activeKey, pool, slot string) (*Owner, error) {
	if _, ok := r.keys[activeKey]; !ok {
		return nil, errors.New("DATA_FABRIC_ROUTING_ACTIVE_KEY_ID must name a configured routing key")
	}
	if !validLabel(slot, 32) || !validPool(pool) {
		return nil, errors.New("routing owner requires a valid slot and transport")
	}
	return &Owner{ring: r, key: activeKey, pool: pool, slot: slot, boot: strings.ToLower(rand.Text())}, nil
}

// Mark attaches routing to an existing opaque query ID or transport nonce.
func (o *Owner) Mark(id string, expiry time.Time) (string, error) {
	if !validLabel(id, 64) || expiry.UnixMilli() <= 0 {
		return "", errInvalidMarker
	}
	payload := strings.Join([]string{
		"v1", o.key, o.ring.deployment, o.pool, o.slot, o.boot,
		strconv.FormatInt(expiry.UnixMilli(), 10), id,
	}, ".")
	return payload + "." + markerEncoding.EncodeToString(authenticate(o.ring.keys[o.key], payload)), nil
}

// Verify identifies an allowlisted slot for a marker with a valid signature,
// transport and expiry. A REST caller also supplies the query ID from the path.
func (r *KeyRing) Verify(marker, pool, id string, now time.Time) (string, error) {
	slot, _, err := r.verify(marker, pool, id, now)
	return slot, err
}

// Verify confirms the marker was issued by this exact process generation.
func (o *Owner) Verify(marker, id string, now time.Time) error {
	slot, boot, err := o.ring.verify(marker, o.pool, id, now)
	if err != nil || slot != o.slot || boot != o.boot {
		return errInvalidMarker
	}
	return nil
}

func (r *KeyRing) verify(marker, pool, id string, now time.Time) (slot, boot string, err error) {
	if len(marker) > maxMarkerLength || !validPool(pool) {
		return "", "", errInvalidMarker
	}
	fields := strings.Split(marker, ".")
	if len(fields) != 9 || fields[0] != "v1" || fields[2] != r.deployment || fields[3] != pool ||
		!validLabel(fields[4], 32) || len(fields[5]) < 26 || !validLabel(fields[5], 64) ||
		!validLabel(fields[7], 64) || (id != "" && fields[7] != id) {
		return "", "", errInvalidMarker
	}
	key, ok := r.keys[fields[1]]
	if !ok || len(fields[8]) != markerEncoding.EncodedLen(sha256.Size) {
		return "", "", errInvalidMarker
	}
	expires, err := strconv.ParseInt(fields[6], 10, 64)
	if err != nil || strconv.FormatInt(expires, 10) != fields[6] || !now.Before(time.UnixMilli(expires)) {
		return "", "", errInvalidMarker
	}
	tag, err := markerEncoding.DecodeString(fields[8])
	if err != nil || markerEncoding.EncodeToString(tag) != fields[8] ||
		!hmac.Equal(tag, authenticate(key, marker[:len(marker)-len(fields[8])-1])) {
		return "", "", errInvalidMarker
	}
	return fields[4], fields[5], nil
}

func authenticate(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func validPool(pool string) bool {
	return pool == REST || pool == MCP
}

func validLabel(label string, maximum int) bool {
	if len(label) == 0 || len(label) > maximum {
		return false
	}
	for _, char := range label {
		if char != '-' && !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}
