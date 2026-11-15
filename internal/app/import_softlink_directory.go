package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Root and Directories contain physical directory identities (no aggregate
// Directory digest/Size). Members are keyed by canonical relative target path.
// A nil leaf Softlink is an explicitly frozen ordinary owned auxiliary file.
// Directory proofs never nest inside a member's Softlink.
type importSoftlinkDirectoryProof struct {
	Root        importSnapshot                           `json:"root"`
	Directories map[string]importSnapshot                `json:"directories"`
	Members     map[string]importSoftlinkDirectoryMember `json:"members"`
	// Existing keeps pre-call entries that this publication does not own. They
	// are validated as unchanged but are never claimed, deleted or rebased.
	Existing *importSoftlinkDirectoryExisting `json:"existing,omitempty"`
}

type importSoftlinkDirectoryExisting struct {
	Root    importSnapshot           `json:"root"`
	Entries []originalDirectoryEntry `json:"entries"`
	Links   map[string]string        `json:"links,omitempty"`
}

type importSoftlinkDirectoryMember struct {
	Snapshot importSnapshot       `json:"snapshot"`
	Softlink *importSoftlinkProof `json:"softlink,omitempty"`
}

func softlinkDirectoryRelative(path string) bool {
	return path != "" && path != "." && path != ".." && !filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.HasPrefix(path, ".."+string(filepath.Separator)) && !strings.ContainsRune(path, '\x00')
}

func softlinkDirectoryIdentity(stamp importSnapshot) importSnapshot {
	stamp.Directory, stamp.Size = "", 0
	return stamp
}

// A completed reader requires the entire frozen tree. Entry-only lifecycle
// readers may observe a missing subset after deletion has begun; their caller
// still compares the returned aggregate unless its durable removal phase allows
// that subset. Neither mode admits a new/replaced entry or follows an unknown link.
func snapshotOwnedSoftlinkDirectory(path string, expected importSnapshot, proof *importSoftlinkProof, requireReferent bool) (importSnapshot, error) {
	if proof == nil || proof.Directory == nil || !importPath(proof.Source) || proof.Source == path || proof.SourceSnapshot.Directory == "" {
		return importSnapshot{}, bad("REVIEW_SOFTLINK_DIRECTORY", "目录软链接证明无效")
	}
	frozen := proof.Directory
	if frozen.Root.Inode == 0 || frozen.Root != softlinkDirectoryIdentity(frozen.Root) {
		return importSnapshot{}, bad("REVIEW_SOFTLINK_DIRECTORY", "目录根身份无效")
	}
	for relative, stamp := range frozen.Directories {
		if !softlinkDirectoryRelative(relative) || stamp.Inode == 0 || stamp != softlinkDirectoryIdentity(stamp) {
			return importSnapshot{}, bad("REVIEW_SOFTLINK_DIRECTORY", "子目录身份无效")
		}
	}
	for relative, member := range frozen.Members {
		_, directory := frozen.Directories[relative]
		if !softlinkDirectoryRelative(relative) || directory || member.Snapshot.Inode == 0 || member.Snapshot.Directory != "" {
			return importSnapshot{}, bad("REVIEW_SOFTLINK_DIRECTORY", "目录成员身份无效")
		}
		if member.Softlink != nil && (member.Softlink.Directory != nil || !importPath(member.Softlink.Source) || member.Softlink.Source == proof.Source || !within(proof.Source, member.Softlink.Source) || member.Softlink.SourceSnapshot.Directory != "") {
			return importSnapshot{}, bad("REVIEW_SOFTLINK_DIRECTORY", "目录成员引用越界或嵌套")
		}
	}
	actual, entries, links, err := scanSoftlinkDirectory(path)
	if err != nil {
		return actual, err
	}
	if !sameInode(actual, expected) || !sameInode(actual, frozen.Root) || requireReferent && softlinkDirectoryIdentity(actual) != frozen.Root {
		return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "受管目录根已变化")
	}
	prior := map[string]originalDirectoryEntry{}
	if frozen.Existing != nil {
		for _, entry := range frozen.Existing.Entries {
			prior[entry.Path] = entry
		}
	}
	directories, members, preserved := 0, 0, 0
	for _, entry := range entries {
		if entry.Directory {
			stamp, ok := frozen.Directories[entry.Path]
			if !ok || !sameInode(stamp, entry.Snapshot) || requireReferent && stamp != entry.Snapshot {
				if before, kept := prior[entry.Path]; !kept || !before.Directory || !sameInode(before.Snapshot, entry.Snapshot) {
					return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "受管子目录已变化或新增")
				}
				preserved++
				continue
			}
			directories++
			continue
		}
		member, ok := frozen.Members[entry.Path]
		if !ok || member.Snapshot != entry.Snapshot {
			if before, kept := prior[entry.Path]; kept && !before.Directory && before.Snapshot == entry.Snapshot && frozen.Existing.Links[entry.Path] == links[entry.Path] {
				preserved++
				continue
			}
			return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "目录包含未知或已变化成员")
		}
		text, link := links[entry.Path]
		if member.Softlink == nil {
			if link {
				return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "普通受管成员被替换为链接")
			}
		} else {
			if !link || text != member.Softlink.Source {
				return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "目录成员链接引用已变化")
			}
			if _, err := snapshotOwnedTransferPath(filepath.Join(path, entry.Path), member.Snapshot, member.Softlink, requireReferent); err != nil {
				return actual, err
			}
		}
		members++
	}
	if requireReferent && (directories != len(frozen.Directories) || members != len(frozen.Members) || preserved != len(prior) || actual != expected) {
		return actual, bad("REVIEW_SOFTLINK_DIRECTORY", "受管目录成员缺失或整体身份变化")
	}
	return actual, nil
}

// Parent directories are created and recorded before publishing a child. A
// crash between mkdir and its record is not an invitation to adopt that path.
func (a *App) prepareSoftlinkDirectoryParents(ctx context.Context, receipt *importReceipt, relative string) error {
	if !softlinkDirectoryRelative(relative) || receipt.Softlink == nil || receipt.Softlink.Directory == nil {
		return bad("REVIEW_SOFTLINK_DIRECTORY", "目录成员映射无效")
	}
	parents := []string{}
	for parent := filepath.Dir(relative); parent != "."; parent = filepath.Dir(parent) {
		parents = append(parents, parent)
	}
	for i := len(parents) - 1; i >= 0; i-- {
		rel := parents[i]
		path := filepath.Join(receipt.Target, rel)
		if _, ok := receipt.Softlink.Directory.Directories[rel]; ok {
			continue
		}
		parent, err := importDir(filepath.Dir(path), false)
		if err != nil {
			return err
		}
		err = unix.Mkdirat(int(parent.Fd()), filepath.Base(path), 0750)
		if err == nil {
			err = parent.Sync()
		}
		parent.Close()
		if err != nil {
			return err
		}
		dir, err := importDir(path, false)
		if err != nil {
			return err
		}
		info, err := dir.Stat()
		dir.Close()
		if err != nil {
			return err
		}
		stamp, err := originalDirectoryIdentity(info)
		if err != nil {
			return err
		}
		receipt.Softlink.Directory.Directories[rel] = stamp
		if err := a.Store.Put(ctx, "import_receipt", receipt.ID, *receipt); err != nil {
			return err
		}
	}
	return nil
}

// During publication, exact child receipts authorize only their own stage or
// destination entries. Directory mtimes may change while those children appear.
func (a *App) guardSoftlinkDirectoryTarget(ctx context.Context, receipt importReceipt) error {
	if receipt.Softlink == nil || receipt.Softlink.Directory == nil || receipt.Softlink.Source != receipt.Source || receipt.Softlink.SourceSnapshot != receipt.SourceSnapshot {
		return bad("REVIEW_SOFTLINK_DIRECTORY", "目录发布证明缺失")
	}
	frozen := *receipt.Softlink.Directory
	for relative, expected := range frozen.Directories {
		dir, err := importDir(filepath.Join(receipt.Target, relative), false)
		if err != nil {
			return err
		}
		info, err := dir.Stat()
		dir.Close()
		if err != nil {
			return err
		}
		actual, err := originalDirectoryIdentity(info)
		if err != nil || !sameInode(actual, expected) {
			return bad("REVIEW_SOFTLINK_DIRECTORY", "发布期间受管子目录被替换")
		}
	}
	frozen.Members = map[string]importSoftlinkDirectoryMember{}
	for _, relative := range receipt.DirectoryFiles {
		target := receipt.directoryTarget(relative)
		if !softlinkDirectoryRelative(relative) || !softlinkDirectoryRelative(target) {
			return bad("REVIEW_SOFTLINK_DIRECTORY", "目录发布路径映射无效")
		}
		var child importReceipt
		err := a.Store.Get(ctx, "import_receipt", receipt.ID+"/directory/"+tokenHash(relative), &child)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if child.ID != receipt.ID+"/directory/"+tokenHash(relative) || child.Source != filepath.Join(receipt.Source, relative) || child.Target != filepath.Join(receipt.Target, target) {
			return bad("REVIEW_RECEIPT", "目录成员收据不一致")
		}
		if child.StageSnapshot.Inode == 0 {
			if child.State == "planned" && child.StorageSoftlink != nil && len(child.StorageSoftlink.Result) > 0 {
				stamp, e := a.verifyStorageSoftlinkStage(ctx, child)
				if e != nil {
					return e
				}
				child.StageSnapshot = stamp
			} else {
				continue
			}
		}
		if child.Stage != ".light-media-"+tokenHash(child.ID)[:32]+".part" || !importPublicationMatchesMethod(child, "softlink") || child.Softlink.Directory != nil {
			return bad("REVIEW_RECEIPT", "目录成员发布身份不一致")
		}
		member := importSoftlinkDirectoryMember{Snapshot: child.StageSnapshot, Softlink: child.Softlink}
		if child.State == "published" || child.State == "staged" {
			frozen.Members[target] = member
		}
		frozen.Members[filepath.Join(filepath.Dir(target), child.Stage)] = member
	}
	proof := *receipt.Softlink
	proof.Directory = &frozen
	_, err := snapshotOwnedSoftlinkDirectory(receipt.Target, receipt.StageSnapshot, &proof, false)
	return err
}

func (a *App) finishSoftlinkDirectoryProof(ctx context.Context, receipt *importReceipt) error {
	if err := a.guardSoftlinkDirectoryTarget(ctx, *receipt); err != nil {
		return err
	}
	actual, entries, _, err := scanSoftlinkDirectory(receipt.Target)
	if err != nil {
		return err
	}
	proof := &importSoftlinkDirectoryProof{Root: softlinkDirectoryIdentity(actual), Directories: map[string]importSnapshot{}, Members: map[string]importSoftlinkDirectoryMember{}}
	for _, entry := range entries {
		if entry.Directory {
			proof.Directories[entry.Path] = entry.Snapshot
		}
	}
	for _, relative := range receipt.DirectoryFiles {
		var child importReceipt
		if err := a.Store.Get(ctx, "import_receipt", receipt.ID+"/directory/"+tokenHash(relative), &child); err != nil {
			return err
		}
		if child.State != "published" || child.Softlink == nil || child.Softlink.Directory != nil {
			return bad("REVIEW_RECEIPT", "目录成员尚未实际发布")
		}
		proof.Members[receipt.directoryTarget(relative)] = importSoftlinkDirectoryMember{Snapshot: child.StageSnapshot, Softlink: child.Softlink}
	}
	receipt.Softlink.Directory = proof
	receipt.StageSnapshot = actual
	_, err = snapshotOwnedSoftlinkDirectory(receipt.Target, actual, receipt.Softlink, true)
	return err
}
