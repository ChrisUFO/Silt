// Package safeio provides size-bounded readers for user-supplied files so a
// hostile synced or shared file cannot drive unbounded allocation before
// validation runs (audit F12). Every JSON/YAML decode of a user-controllable
// file routes its read through ReadFileMax.
package safeio

import (
	"fmt"
	"io"
	"os"
)

// ReadFileMax reads path into memory, failing if the file exceeds max bytes.
// The bound is enforced via an io.LimitReader that permits one byte beyond
// max, so a file of exactly max bytes is accepted and any byte over is a hard
// error. This caps the allocation that precedes a json/yaml Unmarshal of a
// user file (audit F12).
func ReadFileMax(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Allow one byte past the cap: retrieving it proves the file is too large.
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s exceeds the %d-byte cap; refusing to parse", path, max)
	}
	return b, nil
}
