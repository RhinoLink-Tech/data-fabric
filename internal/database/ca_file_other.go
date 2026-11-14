//go:build !unix

package database

// Custom CA file loading is unavailable on platforms without the secure
// no-follow/non-blocking opener. System trust remains available.
func readOperatorCA(string) ([]byte, error) { return nil, ErrEgressDenied }
