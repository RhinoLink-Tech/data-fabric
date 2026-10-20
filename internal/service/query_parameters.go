package service

import (
	"encoding/json"
	"fmt"
	"math"
)

// maxFetchParameters bounds the ordered parameter list. It exists so a
// malformed request cannot push an unbounded slice into the driver (#21).
const maxFetchParameters = 64

// normalizeFetchParameters validates and normalizes ordered query parameters
// for binding (#21).
//
// JSON numbers decoded with UseNumber keep their exact digits: an integer
// that fits int64 binds as int64 (large integers survive verbatim instead of
// degrading to float64), anything else binds as float64 when finite. JSON
// scalars map directly — null to SQL NULL, booleans, strings.
//
// Composite values are rejected: drivers cannot bind maps, slices, or
// structs, and accepting them would invite serializing them back into the
// SQL text. Error messages carry the parameter index and Go type only —
// never the value — so a rejected request cannot leak its own payload.
func normalizeFetchParameters(params []interface{}) ([]interface{}, error) {
	if len(params) == 0 {
		return nil, nil
	}
	if len(params) > maxFetchParameters {
		return nil, fmt.Errorf("too many query parameters: %d, at most %d are supported", len(params), maxFetchParameters)
	}

	normalized := make([]interface{}, len(params))
	for i, param := range params {
		value, err := normalizeFetchParameter(i, param)
		if err != nil {
			return nil, err
		}
		normalized[i] = value
	}
	return normalized, nil
}

func normalizeFetchParameter(index int, param interface{}) (interface{}, error) {
	switch v := param.(type) {
	case nil:
		return nil, nil
	case bool, string:
		return v, nil
	case json.Number:
		return normalizeJSONNumber(index, v)
	case int:
		return int64(v), nil
	case int8, int16, int32, int64:
		return v, nil
	case float32:
		return normalizeFloat(index, float64(v))
	case float64:
		return normalizeFloat(index, v)
	default:
		return nil, fmt.Errorf("query parameter %d has unsupported type %T: use null, boolean, number, or string", index, param)
	}
}

func normalizeJSONNumber(index int, v json.Number) (interface{}, error) {
	if i, err := v.Int64(); err == nil {
		return i, nil
	}
	f, err := v.Float64()
	if err != nil {
		return nil, fmt.Errorf("query parameter %d is not a valid number", index)
	}
	return normalizeFloat(index, f)
}

func normalizeFloat(index int, v float64) (interface{}, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, fmt.Errorf("query parameter %d is not a finite number", index)
	}
	return v, nil
}
