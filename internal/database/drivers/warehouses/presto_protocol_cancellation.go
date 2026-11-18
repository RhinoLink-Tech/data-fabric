package warehouses

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxCapturedProtocolPrefix = 64 * 1024

type queryCancellationRoundTripper struct {
	next          http.RoundTripper
	coordinator   *url.URL
	forceHTTPS    bool
	mu            sync.Mutex
	activeQueries map[context.Context]*trackedProtocolQuery
}

type trackedProtocolQuery struct {
	headers             http.Header
	nextURI             string
	inFlight            int
	inFlightFetches     int
	cancellationPending bool
	cancelDuringFetch   bool
	cancelSent          bool
	stopAfterFunc       func() bool
}

type replayResponseBody struct {
	io.Reader
	io.Closer
}

func newQueryCancellationRoundTripper(next http.RoundTripper, endpoint *url.URL) *queryCancellationRoundTripper {
	coordinator := *endpoint
	forceHTTPS := endpointUsesTLS(endpoint)
	if forceHTTPS {
		coordinator.Scheme = "https"
	}
	return &queryCancellationRoundTripper{
		next:          next,
		coordinator:   &coordinator,
		forceHTTPS:    forceHTTPS,
		activeQueries: make(map[context.Context]*trackedProtocolQuery),
	}
}

func (rt *queryCancellationRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := request.Context()
	isStatement := request.Method == http.MethodPost && strings.TrimRight(request.URL.Path, "/") == "/v1/statement"

	rt.mu.Lock()
	query := rt.activeQueries[ctx]
	if isStatement {
		query = &trackedProtocolQuery{headers: request.Header.Clone(), inFlight: 1}
		rt.activeQueries[ctx] = query
		query.stopAfterFunc = context.AfterFunc(ctx, func() {
			rt.requestCancellation(ctx, query)
		})
	} else if query != nil {
		query.inFlight++
		if request.Method == http.MethodGet {
			query.inFlightFetches++
		}
	}
	rt.mu.Unlock()

	response, requestErr := rt.next.RoundTrip(request)
	if query == nil {
		return response, requestErr
	}

	nextURI := ""
	nextURIFound := false
	responseComplete := false
	if response != nil && response.Body != nil {
		var prefix []byte
		prefix, nextURI, nextURIFound, responseComplete = captureNextURI(response.Body)
		response.Body = replayResponseBody{
			Reader: io.MultiReader(bytes.NewReader(prefix), response.Body),
			Closer: response.Body,
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			// Keep the previous nextUri on a later-page failure so a subsequent
			// context cancellation can still stop the coordinator query.
			responseComplete = false
			nextURIFound = false
			nextURI = ""
		}
	}

	cancelURI, stopAfterFunc := rt.finishRequest(ctx, query, request.Method, nextURI, nextURIFound, responseComplete)
	if stopAfterFunc != nil {
		stopAfterFunc()
	}
	if cancelURI != "" {
		go rt.cancelQuery(query, cancelURI)
	}
	return response, requestErr
}

func captureNextURI(body io.ReadCloser) ([]byte, string, bool, bool) {
	if body == nil {
		return nil, "", false, false
	}
	var prefix []byte
	buffer := make([]byte, 2048)
	for len(prefix) < maxCapturedProtocolPrefix {
		n, readErr := body.Read(buffer)
		if n > 0 {
			prefix = append(prefix, buffer[:n]...)
			if uri, found := decodeNextURI(prefix); found {
				return prefix, uri, true, false
			}
		}
		if readErr != nil {
			return prefix, "", false, readErr == io.EOF
		}
		if n == 0 {
			return prefix, "", false, false
		}
	}
	return prefix, "", false, false
}

func decodeNextURI(body []byte) (string, bool) {
	depth := 0
	for position := 0; position < len(body); {
		switch body[position] {
		case '{', '[':
			depth++
			position++
		case '}', ']':
			depth--
			position++
		case '"':
			key, end, ok := decodeJSONString(body, position)
			if !ok {
				return "", false
			}
			position = end
			if depth != 1 {
				continue
			}
			for position < len(body) && isProtocolJSONSpace(body[position]) {
				position++
			}
			if position >= len(body) || body[position] != ':' {
				continue
			}
			position++
			for position < len(body) && isProtocolJSONSpace(body[position]) {
				position++
			}
			if key != "nextUri" || position >= len(body) {
				continue
			}
			if bytes.HasPrefix(body[position:], []byte("null")) {
				return "", true
			}
			if body[position] != '"' {
				return "", false
			}
			value, _, ok := decodeJSONString(body, position)
			return value, ok
		default:
			position++
		}
	}
	return "", false
}

func decodeJSONString(body []byte, start int) (string, int, bool) {
	position := start + 1
	for position < len(body) {
		switch body[position] {
		case '\\':
			position += 2
		case '"':
			var value string
			if err := json.Unmarshal(body[start:position+1], &value); err != nil {
				return "", 0, false
			}
			return value, position + 1, true
		default:
			position++
		}
	}
	return "", 0, false
}

func isProtocolJSONSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func (rt *queryCancellationRoundTripper) requestCancellation(ctx context.Context, query *trackedProtocolQuery) {
	var cancelURI string
	var stopAfterFunc func() bool
	rt.mu.Lock()
	if rt.activeQueries[ctx] == query {
		query.cancellationPending = true
		if query.inFlightFetches > 0 {
			// The Presto client sends DELETE itself after an in-flight result
			// fetch notices cancellation. Avoid issuing a duplicate DELETE.
			query.cancelDuringFetch = true
		}
		if query.inFlight == 0 && query.nextURI != "" && !query.cancelDuringFetch && !query.cancelSent {
			query.cancelSent = true
			cancelURI = query.nextURI
			delete(rt.activeQueries, ctx)
			stopAfterFunc = query.stopAfterFunc
		} else if query.inFlight == 0 {
			delete(rt.activeQueries, ctx)
			stopAfterFunc = query.stopAfterFunc
		}
	}
	rt.mu.Unlock()
	if stopAfterFunc != nil {
		stopAfterFunc()
	}
	if cancelURI != "" {
		go rt.cancelQuery(query, cancelURI)
	}
}

func (rt *queryCancellationRoundTripper) finishRequest(
	ctx context.Context,
	query *trackedProtocolQuery,
	method string,
	nextURI string,
	nextURIFound bool,
	responseComplete bool,
) (string, func() bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if query.inFlight > 0 {
		query.inFlight--
	}
	if method == http.MethodGet && query.inFlightFetches > 0 {
		query.inFlightFetches--
	}
	if nextURIFound {
		query.nextURI = nextURI
	} else if responseComplete {
		query.nextURI = ""
	}

	var cancelURI string
	var stopAfterFunc func() bool
	if query.cancellationPending && query.inFlight == 0 {
		if query.nextURI != "" && !query.cancelDuringFetch && !query.cancelSent {
			query.cancelSent = true
			cancelURI = query.nextURI
		}
		delete(rt.activeQueries, ctx)
		stopAfterFunc = query.stopAfterFunc
	} else if query.inFlight == 0 && query.nextURI == "" {
		delete(rt.activeQueries, ctx)
		stopAfterFunc = query.stopAfterFunc
	}
	return cancelURI, stopAfterFunc
}

func (rt *queryCancellationRoundTripper) cancelQuery(query *trackedProtocolQuery, nextURI string) {
	target, err := rt.coordinator.Parse(nextURI)
	if err != nil {
		return
	}
	if rt.forceHTTPS && target.Scheme == "http" {
		target.Scheme = "https"
	}
	// The nextUri is supplied by the coordinator; only forward its query
	// credentials back to that same configured origin.
	if target.User != nil || !strings.EqualFold(target.Scheme, rt.coordinator.Scheme) ||
		!strings.EqualFold(target.Host, rt.coordinator.Host) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), nil)
	if err != nil {
		return
	}
	request.Header = query.headers.Clone()
	response, err := rt.next.RoundTrip(request)
	if err != nil {
		return
	}
	if response.Body != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
}
