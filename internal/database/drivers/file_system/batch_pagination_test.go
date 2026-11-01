package file_system

import (
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
)

// These drivers read files rather than querying a SQL engine, so there is no
// LIMIT/OFFSET to push into the source. Half of them used to be unimplemented
// stubs that panicked, which is reachable for every one of these types through
// ConnectionPool.ApplyBatchPagination.
func TestApplyBatchPaginationDoesNotPanic(t *testing.T) {
	const query = "SELECT * FROM events LIMIT 100"

	driversUnderTest := map[string]drivers.SQLQueryPaginater{
		"hdfs-avro":                &HDFSAvroDriver{},
		"hdfs-parquet":             &HDFSParquetDriver{},
		"hdfs-csv":                 &HDFSCSVDriver{},
		"hdfs-text":                &HDFSTextDriver{},
		"hdfs-parquet-compression": &HDFSParquetCompressionDriver{},
		"ozone-parquet":            &OzoneParquetDriver{},
		"ozone-text":               &OzoneTextDriver{},
		"ozone-avro":               &OzoneAvroDriver{},
	}

	for name, driver := range driversUnderTest {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ApplyBatchPagination panicked: %v", r)
				}
			}()

			got, err := driver.ApplyBatchPagination(query, 100, 0)
			if err != nil {
				t.Fatalf("ApplyBatchPagination returned error: %v", err)
			}
			want := "SELECT * FROM (" + query + ") AS __data_fabric_page LIMIT 100 OFFSET 0"
			if got != want {
				t.Errorf("ApplyBatchPagination = %q, want %q", got, want)
			}
		})
	}
}
