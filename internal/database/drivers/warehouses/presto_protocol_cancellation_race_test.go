package warehouses

import (
	"context"
	"net/url"
	"testing"
)

// The AfterFunc callback may run after the canceled fetch has returned from
// RoundTrip. In that order the Presto client owns the cancellation DELETE.
func TestCanceledFetchBeforeAfterFuncDoesNotScheduleDuplicateCancellation(t *testing.T) {
	endpoint, _ := url.Parse("http://localhost:8080")
	rt := newQueryCancellationRoundTripper(nil, endpoint)
	ctx, cancel := context.WithCancel(context.Background())
	query := &trackedProtocolQuery{nextURI: "/v1/next/query", inFlight: 1, inFlightFetches: 1}
	rt.activeQueries[ctx] = query
	cancel()
	if uri, _ := rt.finishRequest(ctx, query, "GET", "", false, false, true); uri != "" {
		t.Fatalf("unexpected cancellation URI from finishRequest: %q", uri)
	}
	rt.requestCancellation(ctx, query)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if !query.cancelDuringFetch || query.cancelSent {
		t.Fatalf("canceled fetch was not retained as client-owned cancellation: fetch=%v sent=%v", query.cancelDuringFetch, query.cancelSent)
	}
}

// A completed successful fetch cannot be assumed to trigger the client's
// error-path DELETE even when the context is canceled before this callback.
func TestCanceledSuccessfulFetchDoesNotClaimClientWillCancel(t *testing.T) {
	endpoint, _ := url.Parse("http://localhost:8080")
	rt := newQueryCancellationRoundTripper(nil, endpoint)
	ctx, cancel := context.WithCancel(context.Background())
	query := &trackedProtocolQuery{nextURI: "/v1/next/query", inFlight: 1, inFlightFetches: 1}
	rt.activeQueries[ctx] = query
	cancel()
	rt.finishRequest(ctx, query, "GET", "/v1/next/page2", true, false, false)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if query.cancelDuringFetch {
		t.Fatal("successful fetch must not claim a client-side cancellation")
	}
}
