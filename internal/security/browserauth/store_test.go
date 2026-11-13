package browserauth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, e := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	v, e := security.NewCredentialVault(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db, v, time.Minute, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestLoginStateBoundAndOneUse(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	state, binding, nonce, challenge, e := s.Begin(ctx)
	if e != nil || challenge == "" {
		t.Fatal(e)
	}
	bad, _ := random()
	if _, _, e = s.Consume(ctx, state, bad); e == nil {
		t.Fatal("wrong browser accepted")
	}
	verifier, got, e := s.Consume(ctx, state, binding)
	if e != nil || verifier == "" || got != nonce {
		t.Fatal("valid state failed")
	}
	if _, _, e = s.Consume(ctx, state, binding); e == nil {
		t.Fatal("replay accepted")
	}
}
func TestSessionAndCSRF(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	actor := Identity{Issuer: "https://id.example", Subject: "user", ExpiresAt: time.Now().Add(time.Minute)}
	cookie, csrf, exp, e := s.CreateSession(ctx, actor)
	if e != nil || !exp.Before(actor.ExpiresAt.Add(time.Second)) {
		t.Fatal(e)
	}
	got, c, e := s.Authenticate(ctx, cookie)
	if e != nil || got.Subject != actor.Subject || c != csrf {
		t.Fatal("session invalid")
	}
	r := httptest.NewRequest("POST", "https://example.test/onboarding/flows", nil)
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("X-CSRF-Token", csrf)
	if !CheckMutation(r, "https://example.test", csrf) {
		t.Fatal("valid mutation rejected")
	}
	r.Header.Set("Origin", "https://attacker.test")
	if CheckMutation(r, "https://example.test", csrf) {
		t.Fatal("bad origin accepted")
	}
	r.Header.Set("Origin", "https://example.test")
	r.Header.Set("X-CSRF-Token", "wrong")
	if CheckMutation(r, "https://example.test", csrf) {
		t.Fatal("bad csrf accepted")
	}
	if e = s.Revoke(ctx, cookie); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Authenticate(ctx, cookie); e == nil {
		t.Fatal("revoked session accepted")
	}
	c1 := SessionCookie(cookie, exp)
	if !c1.Secure || !c1.HttpOnly || c1.Path != "/" || c1.Domain != "" {
		t.Fatal("unsafe cookie")
	}
}
func TestCapacityBoundsAndCleanup(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ms, e := now(s.db)
	if e != nil {
		t.Fatal(e)
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if e := capacity(tx, &loginRecord{}, 1, ms); e != nil {
			return e
		}
		return tx.Create(&loginRecord{ID: "only", Binding: "binding", Encrypted: "encrypted", Expires: ms + 5000}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.db.Transaction(func(tx *gorm.DB) error { return capacity(tx, &loginRecord{}, 1, ms) })
	if err == nil {
		t.Fatal("capacity not enforced")
	}
	err = s.db.Transaction(func(tx *gorm.DB) error { return capacity(tx, &loginRecord{}, 1, ms+6000) })
	if err != nil {
		t.Fatal("expired record not cleaned", err)
	}
	_ = ctx
}

func TestPurgeBoundedKeepsLiveRecords(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ms, err := now(s.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expired1", "expired2", "live"} {
		exp := ms - 1000
		if id == "live" {
			exp = ms + 60000
		}
		if err = s.db.Create(&loginRecord{ID: id, Binding: "x", Encrypted: "x", Expires: exp}).Error; err != nil {
			t.Fatal(err)
		}
		if err = s.db.Create(&sessionRecord{ID: id, Encrypted: "x", Expires: exp}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if s.PurgeBounded(ctx, 0) == nil || s.PurgeBounded(ctx, 1001) == nil {
		t.Fatal("invalid limit accepted")
	}
	if err = s.PurgeBounded(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{&loginRecord{}, &sessionRecord{}} {
		var count int64
		if err = s.db.Model(v).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("unexpected count %d: %v", count, err)
		}
	}
	if err = s.PurgeBounded(ctx, 1); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{&loginRecord{}, &sessionRecord{}} {
		var ids []string
		if err = s.db.Model(v).Pluck("id", &ids).Error; err != nil || len(ids) != 1 || ids[0] != "live" {
			t.Fatalf("live row lost: %v %v", ids, err)
		}
	}
}
