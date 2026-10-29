package object_storage

import (
	"bytes"
	"fmt"

	"github.com/xitongsys/parquet-go/source"
)

// parquetMemoryFile adapts one downloaded object to parquet-go's seekable file
// interface. The underlying bytes are immutable and can be shared by readers.
type parquetMemoryFile struct {
	name   string
	data   []byte
	reader *bytes.Reader
	closed bool
}

func newParquetMemoryFile(name string, data []byte) *parquetMemoryFile {
	return &parquetMemoryFile{name: name, data: data, reader: bytes.NewReader(data)}
}

func (f *parquetMemoryFile) Read(p []byte) (int, error) {
	if f == nil || f.closed {
		return 0, fmt.Errorf("Parquet object reader is closed")
	}
	return f.reader.Read(p)
}

func (f *parquetMemoryFile) Seek(offset int64, whence int) (int64, error) {
	if f == nil || f.closed {
		return 0, fmt.Errorf("Parquet object reader is closed")
	}
	return f.reader.Seek(offset, whence)
}

func (*parquetMemoryFile) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write is not supported for a read-only Parquet object")
}

func (f *parquetMemoryFile) Close() error {
	if f != nil {
		f.closed = true
	}
	return nil
}

func (f *parquetMemoryFile) Open(name string) (source.ParquetFile, error) {
	if f == nil || f.closed {
		return nil, fmt.Errorf("Parquet object reader is closed")
	}
	if name != "" && name != f.name {
		return nil, fmt.Errorf("external Parquet column file %q is not supported", name)
	}
	return newParquetMemoryFile(f.name, f.data), nil
}

func (*parquetMemoryFile) Create(string) (source.ParquetFile, error) {
	return nil, fmt.Errorf("create is not supported for a read-only Parquet object")
}
