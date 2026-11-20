package acceptance

import (
	"encoding/json"
	"testing"
)

func TestReadTrinoNoCountRequiresExplicitUnknownTotalAndContinuation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"continuing", `{"queryId":"q","slug":"s","token":"t","entries":[[1]],"countMode":"none","totalCount":null,"hasMore":true,"nextUri":"/next"}`, true},
		{"final", `{"queryId":"q","entries":[],"countMode":"none","totalCount":null,"hasMore":false}`, true},
		{"missing_total", `{"countMode":"none","hasMore":false}`, false},
		{"zero_total", `{"countMode":"none","totalCount":0,"hasMore":false}`, false},
		{"missing_has_more", `{"countMode":"none","totalCount":null}`, false},
		{"mismatch", `{"countMode":"none","totalCount":null,"hasMore":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := readTrinoNoCount(reply{data: json.RawMessage(tc.body)}, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v valid=%v", err, tc.valid)
			}
			if tc.valid && page.QueryID != "q" {
				t.Fatalf("embedded continuation fields lost: %+v", page)
			}
		})
	}
}
