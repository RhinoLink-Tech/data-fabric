package service

import (
	"encoding/base64"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// The fetch wire representation (#123, #24): batch results in both execution
// modes and every batch — first, continuation, and streamed chunks — encode
// scanned driver values through encodeFetchValue so the JSON a client
// receives is identical whichever path produced it:
//
//   - SQL NULL stays nil and marshals as JSON null — never the text "NULL",
//     which is indistinguishable from a column that actually holds "NULL"
//   - []byte from a text-typed column becomes a string: MySQL and PostgreSQL
//     deliver TEXT/VARCHAR/DECIMAL values as bytes and text must stay text
//   - []byte from a binary-typed column becomes standard base64: raw bytes
//     can contain non-UTF-8 sequences that string conversion would corrupt
//   - everything else (int64, float64, time.Time, bool) passes through with
//     the driver's precision: int64 stays an exact JSON number and DECIMAL
//     arrives as its exact textual form via the text rule above

// encodeFetchRow converts a scanned row in place into the fetch wire
// representation, using the result set's column types to tell binary from
// text bytes.
func encodeFetchRow(row []interface{}, columns []model.ColumnInfo) {
	for i := range row {
		columnType := model.TypeUnknown
		if i < len(columns) {
			columnType = columns[i].Type
		}
		row[i] = encodeFetchValue(row[i], columnType)
	}
}

// encodeFetchValue converts one scanned value. A missing or unknown column
// type treats bytes as text, matching the text-heavy result sets that drove
// the original conversion.
func encodeFetchValue(value interface{}, columnType model.StandardizedType) interface{} {
	if value == nil {
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return value
	}
	if columnType == model.TypeBinary {
		return base64.StdEncoding.EncodeToString(bytes)
	}
	return string(bytes)
}
