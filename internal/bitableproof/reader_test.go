package bitableproof

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func limits() Limits {
	return Limits{PageSize: 2, MaxPages: 5, MaxRows: 10, MaxBytes: 4096, MaxSessions: 10, Duration: 3 * time.Second, SessionTTL: time.Second}
}
func scope() Scope {
	return Scope{Caller: "alice", Tenant: "a", Source: "sales", Table: "leads", View: "follow-up"}
}
func reader(t *testing.T, h http.HandlerFunc, l Limits) (*Reader, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(h)
	r, err := New(server.URL+"/records", l)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return r, server
}
func TestReadPagesBoundToIdentityAndQuery(t *testing.T) {
	var calls atomic.Int32
	r, _ := reader(t, func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Method != http.MethodGet || req.URL.Query().Get("table") != "leads" || req.URL.Query().Get("view") != "follow-up" {
			t.Error("unexpected fake-provider request")
		}
		switch req.URL.Query().Get("page_token") {
		case "":
			fmt.Fprint(w, `{"records":[{"record_id":"1","status":"new","private_phone":"hidden"},{"record_id":"2"}],"has_more":true,"page_token":"provider-secret"}`)
		case "provider-secret":
			fmt.Fprint(w, `{"records":[{"record_id":"3","status":"changed","created_at":1730000000000}],"has_more":false,"page_token":""}`)
		default:
			http.Error(w, "provider-secret", 500)
		}
	}, limits())
	first, err := r.Read(context.Background(), scope(), "")
	if err != nil || first.Complete || first.Truncated || first.Next == "" || len(first.Records) != 2 {
		t.Fatalf("first page: %+v %v", first, err)
	}
	if first.Records[1].Status != nil || first.Records[1].CreatedAt != nil {
		t.Fatal("missing values became known")
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "provider-secret") || strings.Contains(string(raw), "hidden") {
		t.Fatal("provider cursor or unapproved field leaked")
	}
	foreign := scope()
	foreign.Caller = "bob"
	if _, err = r.Read(context.Background(), foreign, first.Next); !errors.Is(err, ErrContinuation) {
		t.Fatalf("foreign caller: %v", err)
	}
	foreign = scope()
	foreign.View = "other"
	if _, err = r.Read(context.Background(), foreign, first.Next); !errors.Is(err, ErrContinuation) {
		t.Fatalf("different query: %v", err)
	}
	second, err := r.Read(context.Background(), scope(), first.Next)
	if err != nil || !second.Complete || second.Truncated || second.Next != "" || len(second.Records) != 1 || second.Records[0].Status == nil || *second.Records[0].Status != "changed" {
		t.Fatalf("second page: %+v %v", second, err)
	}
	if _, err = r.Read(context.Background(), scope(), first.Next); !errors.Is(err, ErrContinuation) {
		t.Fatalf("replay: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("provider calls=%d", calls.Load())
	}
}

func TestReadEmptyPagesAndTruncation(t *testing.T) {
	lim := limits()
	lim.MaxPages = 2
	r, _ := reader(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page_token") == "" {
			fmt.Fprint(w, `{"records":[],"has_more":true,"page_token":"p2"}`)
			return
		}
		fmt.Fprint(w, `{"records":[{"record_id":"1"}],"has_more":true,"page_token":"p3"}`)
	}, lim)
	first, err := r.Read(context.Background(), scope(), "")
	if err != nil || first.Complete || first.Next == "" || len(first.Records) != 0 {
		t.Fatalf("empty page: %+v %v", first, err)
	}
	last, err := r.Read(context.Background(), scope(), first.Next)
	if err != nil || last.Complete || !last.Truncated || last.Next != "" || len(last.Records) != 1 {
		t.Fatalf("truncated page: %+v %v", last, err)
	}
}

func TestRejectsExpiredAndConcurrentReplay(t *testing.T) {
	lim := limits()
	lim.SessionTTL = 30 * time.Millisecond
	r, _ := reader(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("page_token") == "" {
			fmt.Fprint(w, `{"records":[],"has_more":true,"page_token":"p2"}`)
			return
		}
		fmt.Fprint(w, `{"records":[],"has_more":false,"page_token":""}`)
	}, lim)
	first, err := r.Read(context.Background(), scope(), "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err = r.Read(context.Background(), scope(), first.Next); !errors.Is(err, ErrContinuation) {
		t.Fatalf("expired: %v", err)
	}
	first, err = r.Read(context.Background(), scope(), "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var success, denied atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := r.Read(context.Background(), scope(), first.Next)
			if e == nil {
				success.Add(1)
			} else if errors.Is(e, ErrContinuation) {
				denied.Add(1)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 || denied.Load() != 1 {
		t.Fatalf("success=%d denied=%d", success.Load(), denied.Load())
	}
}

func TestProviderErrorsAreBoundedAndSanitized(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", 401, "secret-marker", ErrPermission},
		{"forbidden", 403, "secret-marker", ErrPermission},
		{"rate", 429, "secret-marker", ErrRateLimited},
		{"server", 500, "secret-marker", ErrUnavailable},
		{"malformed", 200, `{"records":[],"page_token":""}`, ErrUnavailable},
		{"missing_records", 200, `{"has_more":false,"page_token":""}`, ErrUnavailable},
		{"missing_cursor", 200, `{"records":[],"has_more":true}`, ErrUnavailable},
		{"extra_json", 200, `{"records":[],"has_more":false} {}`, ErrUnavailable},
		{"oversized", 200, strings.Repeat("x", 5000), ErrBudget},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := reader(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }, limits())
			page, err := r.Read(context.Background(), scope(), "")
			if !errors.Is(err, tc.want) || strings.Contains(fmt.Sprint(err), "secret-marker") || page.Complete {
				t.Fatalf("page=%+v error=%v", page, err)
			}
		})
	}
}

func TestCancellationAndDeadline(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			lim := limits()
			lim.Duration = 80 * time.Millisecond
			started := make(chan struct{}, 1)
			r, _ := reader(t, func(w http.ResponseWriter, req *http.Request) { started <- struct{}{}; <-req.Context().Done() }, lim)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := r.Read(ctx, scope(), ""); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not start")
			}
			if cancelEarly {
				cancel()
			}
			select {
			case err := <-done:
				if cancelEarly && !errors.Is(err, context.Canceled) || !cancelEarly && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("unexpected cancellation %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("request did not stop")
			}
		})
	}
}
func TestRejectsNonLocalAndRedirect(t *testing.T) {
	for _, u := range []string{"https://127.0.0.1:80/records", "http://example.com:80/records", "http://169.254.169.254:80/records", "http://127.0.0.1:80/records?token=secret", "http://user:pass@127.0.0.1:80/records"} {
		if _, err := New(u, limits()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q", u)
		}
	}
	r, _ := reader(t, func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://example.com/", http.StatusFound)
	}, limits())
	if _, err := r.Read(context.Background(), scope(), ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("redirect: %v", err)
	}
}
