package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

// ReadInstalled revalidates the receipt and executable bytes on each resolver
// lookup. It performs no writes, executes nothing, and never grants authority
// from a receipt declaration. Root must remain exclusively owned by the host;
// callers must serialize this lookup and subsequent execution with updates.
// codeID is the owned directory key, potentially a clone instance. Validate MUST
// compare Manifest.PluginID and Version with the expected installation identity;
// the directory name is not the underlying business plugin identity.
func ReadInstalled(root, codeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	operationMu.Lock()
	defer operationMu.Unlock()
	return readInstalled(root, codeID, validate)
}

func readInstalled(root, codeID string, validate func(plugins.Manifest, string) error) (Acquired, error) {
	bad := func(err error) (Acquired, error) {
		return Acquired{}, fmt.Errorf("installed plugin validation: %w", err)
	}
	if root == "" || !idPattern.MatchString(codeID) || validate == nil {
		return bad(errors.New("root, valid ID and trusted validator required"))
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return bad(err)
	}
	cur := filepath.VolumeName(abs) + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(abs, cur), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			return bad(err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return bad(errors.New("unsafe root ancestor"))
		}
	}
	target := filepath.Join(abs, codeID)
	if err = safeExistingTree(target); err != nil {
		return bad(err)
	}
	receiptFile, err := os.Open(filepath.Join(target, ".acquired.json"))
	if err != nil {
		return bad(err)
	}
	receiptBytes, err := io.ReadAll(io.LimitReader(receiptFile, MaxIndexBytes+1))
	err = errors.Join(err, receiptFile.Close())
	if err != nil {
		return bad(err)
	}
	if int64(len(receiptBytes)) > MaxIndexBytes {
		return bad(errors.New("oversized receipt"))
	}
	var receipt struct {
		Manifest     plugins.Manifest `json:"manifest"`
		SHA256       string           `json:"sha256"`
		HostContract string           `json:"host_contract"`
	}
	dec := json.NewDecoder(strings.NewReader(string(receiptBytes)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&receipt); err != nil {
		return bad(err)
	}
	if dec.Decode(new(any)) != io.EOF {
		return bad(errors.New("trailing receipt data"))
	}
	if !safeName(receipt.Manifest.Entry) || !validDigest(receipt.SHA256) || len(receipt.HostContract) > 256 {
		return bad(errors.New("invalid receipt identity, path or hash"))
	}
	if err = receipt.Manifest.Validate(); err != nil {
		return bad(err)
	}
	if err = validate(receipt.Manifest, receipt.HostContract); err != nil {
		return bad(err)
	}
	executable := filepath.Join(target, filepath.FromSlash(receipt.Manifest.Entry))
	info, err := os.Lstat(executable)
	if err != nil {
		return bad(err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxExpandedBytes {
		return bad(errors.New("invalid executable type or size"))
	}
	f, err := os.Open(executable)
	if err != nil {
		return bad(err)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxExpandedBytes+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return bad(err)
	}
	if int64(len(b)) > MaxExpandedBytes || digest(b) != strings.ToLower(receipt.SHA256) {
		return bad(errors.New("installed executable digest mismatch"))
	}
	return Acquired{Manifest: receipt.Manifest, ExecutablePath: executable, SHA256: digest(b), HostContract: receipt.HostContract, Command: plugins.Command{Path: executable, Dir: target}}, nil
}
