//go:build unix

package database

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
)

// Open once without following the final symlink or blocking on FIFOs, then
// validate and read the same descriptor (no Lstat/Open replacement window).
func readOperatorCA(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, ErrEgressDenied
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, ErrEgressDenied
	}
	return data, nil
}
