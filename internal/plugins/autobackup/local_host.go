package autobackup

import (
	"bytes"
	"encoding/json"
	"math/big"
	"os"
)

// LocalRetentionCount retains the source's falsey bypass and Python int
// conversion. The host invokes it after trying to create the archive.
func LocalRetentionCount(raw json.RawMessage) (*big.Int, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || !truth(value) {
		return nil, err
	}
	return integer(value)
}

// LocalBackupCreationTime is the existing Unix getctime adapter.
func LocalBackupCreationTime(info os.FileInfo) (float64, error) {
	return creationTime(info)
}
