// Local-only synthetic XML/JPEG fixture generator. No NAS API or video execution.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"light-media/internal/media"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func validate(id, root string) error {
	if !uuid.MatchString(id) {
		return errors.New("EXPLICIT_UUID_REQUIRED")
	}
	abs, e := filepath.Abs(root)
	if e != nil {
		return errors.New("INVALID_ROOT")
	}
	work, e := filepath.Abs("work")
	if e != nil {
		return errors.New("INVALID_ROOT")
	}
	rel, e := filepath.Rel(work, abs)
	if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("ROOT_MUST_BE_UNDER_LOCAL_WORK")
	}
	parent := filepath.Dir(abs)
	resolved, e := filepath.EvalSymlinks(parent)
	if e != nil || resolved != parent {
		return errors.New("PARENT_MISSING_OR_SYMLINK")
	}
	return nil
}
func exclusive(p string, b []byte) error {
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func generate(id, root string, apply bool) error {
	if e := validate(id, root); e != nil {
		return e
	}
	if !apply {
		return nil
	}
	if e := os.Mkdir(root, 0700); e != nil {
		return errors.New("ROOT_EXISTS_OR_UNAVAILABLE")
	}
	ns := "light-media-m0-test-" + id
	ids := func(v string) []media.UniqueID {
		return []media.UniqueID{{Type: "lightmedia-m0", Default: true, Value: ns + ":" + v}}
	}
	actors := []media.Actor{{Name: ns + "-Actor001", Role: ns + "-Role001", Order: 0}, {Name: ns + "-Actor002", Role: ns + "-Role002", Order: 1}}
	records := []struct {
		rel      string
		metadata media.NFOMetadata
	}{
		{ns + "-Movie (2024)/" + ns + "-Movie (2024).nfo", media.NFOMetadata{Kind: "movie", Title: ns + " 合成电影 <验证> & 本地NFO", OriginalTitle: "M0 Synthetic Movie", Year: 2024, Plot: "M0 隔离合成样本，不是真实影视条目。", UniqueIDs: ids("900000101"), Actors: actors}},
		{ns + "-Series/tvshow.nfo", media.NFOMetadata{Kind: "tvshow", Title: ns + " 合成普通剧", Year: 2024, UniqueIDs: ids("900000102"), Actors: actors}},
		{ns + "-Series/Season 1/" + ns + "-Series S01E02.nfo", media.NFOMetadata{Kind: "episode", Title: ns + " 合成第二集", ShowTitle: ns + " 合成普通剧", Season: 1, Episode: 2, Aired: "2024-01-02", UniqueIDs: ids("900000103"), Actors: actors}},
		{ns + "-Animation/tvshow.nfo", media.NFOMetadata{Kind: "tvshow", Title: ns + " 合成国漫", Year: 2024, UniqueIDs: ids("900000104"), Actors: actors}},
		{ns + "-Animation/Season 2/" + ns + "-Animation S02E01.nfo", media.NFOMetadata{Kind: "episode", Title: ns + " 合成国漫映射第一集", ShowTitle: ns + " 合成国漫", Season: 2, Episode: 1, Aired: "2024-01-02", UniqueIDs: ids("900000105"), Actors: actors}},
	}
	for _, v := range records {
		b, e := media.EncodeNFO(v.metadata)
		if e != nil {
			return errors.New("NFO_ENCODE_FAILED")
		}
		p := filepath.Join(root, v.rel)
		if os.MkdirAll(filepath.Dir(p), 0700) != nil || exclusive(p, b) != nil {
			return errors.New("FIXTURE_WRITE_FAILED")
		}
	}
	for i, suffix := range []string{"-Movie (2024)", "-Series", "-Animation"} {
		for _, name := range []string{"poster.jpg", "fanart.jpg"} {
			img := image.NewRGBA(image.Rect(0, 0, 64, 64))
			for y := 0; y < 64; y++ {
				for x := 0; x < 64; x++ {
					img.Set(x, y, color.RGBA{uint8(50 + i*80), uint8(40 + x), uint8(40 + y), 255})
				}
			}
			f, e := os.OpenFile(filepath.Join(root, ns+suffix, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return errors.New("FIXTURE_WRITE_FAILED")
			}
			e = jpeg.Encode(f, img, &jpeg.Options{Quality: 85})
			ce := f.Close()
			if e != nil || ce != nil {
				return errors.New("FIXTURE_WRITE_FAILED")
			}
		}
	}
	return nil
}
func main() {
	flags := flag.NewFlagSet("nfo-fixtures", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	id := flags.String("experiment-id", "", "explicit lowercase UUID")
	root := flags.String("out-dir", "work/nfo-live-fixture-v2", "new local output root under work")
	apply := flags.Bool("apply", false, "create local synthetic XML/JPEG")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "code": "INVALID_FLAGS"})
		os.Exit(2)
	}
	if e := generate(*id, *root, *apply); e != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "failed", "code": e.Error()})
		os.Exit(1)
	}
	status := "dry_run"
	if *apply {
		status = "generated"
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"status": status, "uuid": *id, "root": *root, "synthetic": true, "video_generated": false, "nfo_count": 5, "jpeg_count": 6})
}
