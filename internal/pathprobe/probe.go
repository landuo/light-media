package pathprobe

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Report struct {
	Evidence          string   `json:"evidence"`
	Status            string   `json:"status"`
	Reason            string   `json:"reason,omitempty"`
	LinkSucceeded     bool     `json:"link_succeeded"`
	SameFile          bool     `json:"same_file"`
	SourceContentKept bool     `json:"source_content_kept"`
	RenameSourceKept  bool     `json:"rename_source_kept"`
	UnlinkSourceKept  bool     `json:"unlink_source_kept"`
	CleanupSucceeded  bool     `json:"cleanup_succeeded"`
	Limitations       []string `json:"limitations"`
}

// Run creates at most three uniquely named small files. It never lists, reads,
// renames or unlinks existing media. Link and every publish use no-overwrite
// operations. Paths and OS errors are not returned, to avoid exposing NAS paths.
func Run(sourceDir, targetDir string) (report Report, returnErr error) {
	report = Report{Evidence: "actual_filesystem_probe", Status: "failed", Limitations: []string{"valid_only_for_tested_directories_and_current_mount", "does_not_prove_cross_subvolume_or_other_mount_compatibility", "hardlink_shares_contents_do_not_modify_media_in_place"}}
	sourceDir, err := directory(sourceDir)
	if err != nil {
		report.Reason = "source_directory_unavailable"
		return report, err
	}
	targetDir, err = directory(targetDir)
	if err != nil {
		report.Reason = "target_directory_unavailable"
		return report, err
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		report.Reason = "random_source_failed"
		return report, err
	}
	name := ".light-media-m0-" + hex.EncodeToString(token)
	source := filepath.Join(sourceDir, name+"-source.tmp")
	target := filepath.Join(targetDir, name+"-target.tmp")
	renamed := filepath.Join(targetDir, name+"-renamed.tmp")
	owned := map[string]os.FileInfo{}
	defer func() {
		clean := true
		for p, identity := range owned {
			info, statErr := os.Lstat(p)
			if os.IsNotExist(statErr) {
				continue
			}
			if statErr != nil || !os.SameFile(identity, info) {
				clean = false
				continue
			}
			if removeErr := os.Remove(p); removeErr != nil {
				clean = false
			}
		}
		report.CleanupSucceeded = clean
		if !clean {
			report.Status, report.Reason = "failed", "owned_file_cleanup_failed"
			returnErr = fmt.Errorf("cleanup_failed")
		}
	}()
	f, err := os.OpenFile(source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		report.Reason = "source_create_failed"
		return report, safeError(err)
	}
	identity, err := f.Stat()
	if err != nil {
		f.Close()
		report.Reason = "source_stat_failed"
		return report, safeError(err)
	}
	owned[source] = identity
	payload := []byte("light-media M0 temporary hardlink probe\n")
	_, writeErr := f.Write(payload)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		report.Reason = "source_write_failed"
		return report, fmt.Errorf("source_write_failed")
	}
	if err = os.Link(source, target); err != nil {
		report.Reason = errorCode(err)
		return report, safeError(err)
	}
	owned[target] = identity
	report.LinkSucceeded = true
	a, aErr := os.Stat(source)
	b, bErr := os.Stat(target)
	if aErr != nil || bErr != nil || !os.SameFile(a, b) {
		report.Reason = "identity_mismatch"
		return report, fmt.Errorf("identity_mismatch")
	}
	report.SameFile = true
	read, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(read, payload) {
		report.Reason = "source_content_mismatch"
		return report, fmt.Errorf("source_content_mismatch")
	}
	report.SourceContentKept = true
	// Portable rename without overwriting: link the new name exclusively, then
	// unlink the old name. Plain os.Rename could replace an unrelated file.
	if err = os.Link(target, renamed); err != nil {
		report.Reason = "rename_publish_failed"
		return report, safeError(err)
	}
	owned[renamed] = identity
	if err = os.Remove(target); err != nil {
		report.Reason = "rename_unlink_failed"
		return report, safeError(err)
	}
	delete(owned, target)
	if !sourceIntact(source, identity, payload) {
		report.Reason = "source_changed_after_rename"
		return report, fmt.Errorf("source_changed")
	}
	report.RenameSourceKept = true
	if err = os.Remove(renamed); err != nil {
		report.Reason = "target_unlink_failed"
		return report, safeError(err)
	}
	delete(owned, renamed)
	if !sourceIntact(source, identity, payload) {
		report.Reason = "source_changed_after_unlink"
		return report, fmt.Errorf("source_changed")
	}
	report.UnlinkSourceKept, report.Status = true, "passed"
	return report, nil
}

func sourceIntact(source string, identity os.FileInfo, payload []byte) bool {
	info, err := os.Stat(source)
	if err != nil || !os.SameFile(identity, info) {
		return false
	}
	data, err := os.ReadFile(source)
	return err == nil && bytes.Equal(data, payload)
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, syscall.EXDEV):
		return "EXDEV"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, os.ErrExist):
		return "exclusive_path_collision"
	default:
		return "filesystem_operation_failed"
	}
}

func safeError(err error) error { return fmt.Errorf("%s", errorCode(err)) }
