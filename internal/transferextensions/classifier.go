// Package transferextensions preserves the original list membership decisions.
// The host owns when each constructor or live-handler snapshot is observed.
package transferextensions

import (
	"errors"
	"path/filepath"
	"strings"

	"light-media/internal/fileextensions"
	"light-media/internal/releasemeta/legacyanime"
)

var ErrDependency = errors.New("transfer extension input or bluray fact is unknown")

type File struct {
	Type        string
	Extension   *string
	BlurayKnown bool
	Bluray      bool
}

type Classifier struct{ snapshot fileextensions.Snapshot }

func New(snapshot fileextensions.Snapshot) (Classifier, error) {
	if !snapshot.Valid() {
		return Classifier{}, ErrDependency
	}
	// Resolve clones all observed lists and bytes; callers cannot mutate decisions.
	cloned, e := fileextensions.Resolve(snapshot.Inputs)
	if e != nil {
		return Classifier{}, e
	}
	return Classifier{cloned}, nil
}
func (c Classifier) has(key string, f File) bool {
	return f.Extension != nil && *f.Extension != "" && c.snapshot.Contains(key, "."+legacyanime.LowerCase(*f.Extension))
}
func (c Classifier) Subtitle(f File) bool { return c.has(fileextensions.SubtitleKey, f) }
func (c Classifier) Audio(f File) bool    { return c.has(fileextensions.AudioKey, f) }
func (c Classifier) Extra(f File) bool    { return c.Subtitle(f) || c.Audio(f) }
func (c Classifier) Allowed(f File) bool  { return c.has(fileextensions.MediaKey, f) || c.Extra(f) }
func (c Classifier) Media(f File) (bool, error) {
	if f.Type == "dir" {
		if !f.BlurayKnown {
			return false, ErrDependency
		}
		return f.Bluray, nil
	}
	return c.has(fileextensions.MediaKey, f), nil
}

// PathFile projects the original local FileItem Path.suffix.lstrip('.'). Unlike
// filepath.Ext, a single leading dot and a trailing dot are not suffixes.
func PathFile(path string) File {
	name := filepath.Base(path)
	ext := ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 && dot < len(name)-1 {
		ext = strings.TrimLeft(name[dot+1:], ".")
	}
	return File{Type: "file", Extension: &ext}
}
