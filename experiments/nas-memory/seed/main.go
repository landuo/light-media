// Seed a fresh, synthetic /healthz fixture. Never open an existing data directory.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"light-media/internal/app"
	"log"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: seed NEW_DATA_DIR")
	}
	dir := os.Args[1]
	if err := os.Mkdir(dir, 0700); err != nil {
		log.Fatal(err)
	}
	src := filepath.Join(dir, "source")
	c := app.Settings{AllowedRoots: []string{src}}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("rule-%d", i)
		p := filepath.Join(src, id)
		if err := os.MkdirAll(p, 0700); err != nil {
			log.Fatal(err)
		}
		c.Directories = append(c.Directories, app.DirectoryRule{ID: id, Name: id, SourcePath: p, Mode: "hardlink", MediaType: "mixed", MonitorType: "none", Enabled: true})
	}
	input, e := json.Marshal(map[string]any{"settings": c, "username": "fixture", "password": rand.Text()})
	if e != nil {
		log.Fatal(e)
	}
	if e = app.Bootstrap(dir, bytes.NewReader(input)); e != nil {
		log.Fatal(e)
	}
	a, e := app.New(app.Options{DataDir: dir, SafeMode: true})
	if e != nil {
		log.Fatal(e)
	}
	states := []string{"baseline", "imported", "observing", "needs_review", "error"}
	for _, r := range c.Directories {
		for j := 0; j < 210; j++ {
			id := fmt.Sprintf("%s-%03d", r.ID, j)
			f := app.DirectoryObservedFile{ID: id, DirectoryID: r.ID, Path: filepath.Join(r.SourcePath, id+".mkv"), State: states[j%5], Reason: "synthetic", LastSeen: time.Unix(int64(1700000000+j), 0).UTC().Format(time.RFC3339)}
			if e = a.Store.Put(context.Background(), "directory_file", id, f); e != nil {
				log.Fatal(e)
			}
		}
	}
	if e = a.Close(); e != nil {
		log.Fatal(e)
	}
}
