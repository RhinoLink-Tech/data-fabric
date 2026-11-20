package model

import (
	"encoding/json"
	"testing"
)

func TestQueryCountModeInput(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		mode        QueryCountMode
		valid       bool
	}{
		{"omitted", `{}`, "", true},
		{"empty", `{"count_mode":""}`, "", true},
		{"exact", `{"count_mode":"exact"}`, QueryCountExact, true},
		{"none", `{"count_mode":"none"}`, QueryCountNone, true},
		{"null", `{"count_mode":null}`, "", false},
		{"number", `{"count_mode":1}`, "", false},
		{"boolean", `{"count_mode":false}`, "", false},
		{"other", `{"count_mode":"fast"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request FetchQueryRequest
			err := json.Unmarshal([]byte(tc.input), &request)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v, valid=%v", err, tc.valid)
			}
			if err == nil && request.CountMode != tc.mode {
				t.Fatalf("mode=%q, want %q", request.CountMode, tc.mode)
			}
		})
	}
	if QueryCountMode("unexpected").Validate() == nil {
		t.Fatal("invalid in-process mode accepted")
	}
}

func TestQueryCountModeResponse(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      QueryCountMode
		next      string
		more      *bool
		wantTotal interface{}
		wantMore  interface{}
		valid     bool
	}{
		{"legacy total remains numeric", "", "", nil, float64(3), nil, true},
		{"unknown final", QueryCountNone, "", boolPointer(false), nil, false, true},
		{"unknown continuing", QueryCountNone, "/next", boolPointer(true), nil, true, true},
		{"missing indicator", QueryCountNone, "", nil, nil, nil, false},
		{"false with continuation", QueryCountNone, "/next", boolPointer(false), nil, nil, false},
		{"true without continuation", QueryCountNone, "", boolPointer(true), nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(FetchQueryResponse{TotalCount: 3, CountMode: tc.mode, HasMore: tc.more, NextURI: tc.next})
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v valid=%v", err, tc.valid)
			}
			if err != nil {
				return
			}
			var got map[string]interface{}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if value, exists := got["totalCount"]; !exists || value != tc.wantTotal {
				t.Fatalf("totalCount=%v present=%v", value, exists)
			}
			if value, exists := got["hasMore"]; value != tc.wantMore || exists != (tc.wantMore != nil) {
				t.Fatalf("hasMore=%v present=%v", value, exists)
			}
			if tc.mode == QueryCountNone && got["countMode"] != "none" {
				t.Fatalf("countMode=%v", got["countMode"])
			}
			if tc.mode == "" {
				if _, exists := got["countMode"]; exists {
					t.Fatal("legacy response changed")
				}
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
