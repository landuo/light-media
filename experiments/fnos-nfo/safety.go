package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const prefix = "light-media-m0-test-"
const parentRoot = "/vol2/1000/media"

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var guidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type library struct {
	GUID     string   `json:"guid"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Dirs     []string `json:"dir_list"`
}
type receipt struct {
	ExperimentID string    `json:"experiment_id"`
	HostDigest   string    `json:"host_digest"`
	Name         string    `json:"name"`
	Root         string    `json:"root"`
	BaselineIDs  []string  `json:"baseline_ids"`
	Baseline     []library `json:"baseline"`
	OwnedGUID    string    `json:"owned_guid,omitempty"`
	Phase        string    `json:"phase"`
}

func newReceipt(id, host string, libs []library) (receipt, error) {
	if !uuidPattern.MatchString(id) {
		return receipt{}, errors.New("INVALID_EXPERIMENT_ID")
	}
	r := receipt{ExperimentID: id, HostDigest: hostDigest(host), Name: prefix + id, Root: path.Join(parentRoot, prefix+id), Phase: "planned", Baseline: libs}
	for _, l := range libs {
		if !guidPattern.MatchString(l.GUID) || len(l.Dirs) == 0 {
			return receipt{}, errors.New("INCOMPLETE_LIBRARY_BASELINE")
		}
		r.BaselineIDs = append(r.BaselineIDs, l.GUID)
	}
	if err := checkIsolation(r, libs); err != nil {
		return receipt{}, err
	}
	return r, nil
}
func hostDigest(host string) string {
	v := sha256.Sum256([]byte(host))
	return hex.EncodeToString(v[:])
}
func checkIsolation(r receipt, libs []library) error {
	for _, l := range libs {
		if l.GUID == r.OwnedGUID && r.OwnedGUID != "" {
			continue
		}
		if l.Name == r.Name {
			return errors.New("EXPERIMENT_NAME_ALREADY_EXISTS")
		}
		if len(l.Dirs) == 0 {
			return errors.New("UNKNOWN_LIBRARY_ROOT")
		}
		for _, d := range l.Dirs {
			if !validAbsolutePath(d) {
				return errors.New("UNKNOWN_LIBRARY_ROOT")
			}
			d = path.Clean(d)
			if d == r.Root || strings.HasPrefix(r.Root, d+"/") || strings.HasPrefix(d, r.Root+"/") || d == "/" {
				return errors.New("LIBRARY_ROOT_OVERLAP")
			}
		}
	}
	return nil
}
func ownLibrary(r receipt, libs []library) (library, error) {
	var found []library
	for _, l := range libs {
		if l.Name == r.Name && l.Category == "Mix" && len(l.Dirs) == 1 && samePath(l.Dirs[0], r.Root) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		return library{}, errors.New("OWNED_LIBRARY_NOT_UNIQUE")
	}
	l := found[0]
	if !guidPattern.MatchString(l.GUID) {
		return library{}, errors.New("INVALID_OWNED_GUID")
	}
	for _, old := range r.BaselineIDs {
		if l.GUID == old {
			return library{}, errors.New("EXISTING_GUID_REFUSED")
		}
	}
	if r.OwnedGUID != "" && r.OwnedGUID != l.GUID {
		return library{}, errors.New("OWNED_GUID_CHANGED")
	}
	r.OwnedGUID = l.GUID
	if err := checkIsolation(r, libs); err != nil {
		return library{}, err
	}
	return l, nil
}
func receiptPath(id string) string { return filepath.Join("work", "fnos-nfo-experiment-"+id+".json") }
func saveReceipt(r receipt) error {
	data, e := json.MarshalIndent(r, "", "  ")
	if e != nil || len(data) > 32768 {
		return errors.New("RECEIPT_ENCODING_FAILED")
	}
	name := receiptPath(r.ExperimentID)
	// No secrets are stored. A receipt must precede the create request to enable
	// reconciliation after an accepted request whose response was lost.
	f, e := os.CreateTemp(filepath.Dir(name), ".fnos-nfo-receipt-*")
	if e != nil {
		return errors.New("RECEIPT_WRITE_FAILED")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil || closeErr != nil {
		return errors.New("RECEIPT_WRITE_FAILED")
	}
	if os.Rename(tmp, name) != nil {
		return errors.New("RECEIPT_WRITE_FAILED")
	}
	return nil
}
func loadReceipt(id, host string) (receipt, error) {
	var r receipt
	if !uuidPattern.MatchString(id) {
		return r, errors.New("INVALID_EXPERIMENT_ID")
	}
	name := receiptPath(id)
	info, e := os.Lstat(name)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 32768 {
		return r, errors.New("RECEIPT_UNAVAILABLE")
	}
	b, e := os.ReadFile(name)
	if e != nil || json.Unmarshal(b, &r) != nil {
		return r, errors.New("RECEIPT_INVALID")
	}
	if r.ExperimentID != id || r.HostDigest != hostDigest(host) || r.Name != prefix+id || r.Root != path.Join(parentRoot, prefix+id) || len(r.BaselineIDs) > 200 || len(r.Baseline) != len(r.BaselineIDs) {
		return r, errors.New("RECEIPT_SCOPE_MISMATCH")
	}
	return r, nil
}

// Canonicalize separators only; dot segments are refused to avoid masking traversal.
func validAbsolutePath(s string) bool {
	if !strings.HasPrefix(s, "/") || strings.ContainsAny(s, "\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return true
}
func samePath(a, b string) bool {
	return validAbsolutePath(a) && validAbsolutePath(b) && path.Clean(a) == path.Clean(b)
}
