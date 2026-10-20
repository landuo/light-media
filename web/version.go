package web

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io/fs"

	"light-media/internal/systemversion"
)

//go:embed package.json
var buildManifest []byte

// FrontendVersion reads the compiled UI's version.txt, with the actual package
// build version as the native FRONTEND_VERSION fallback.
func FrontendVersion() (string, error) {
	var metadata struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(buildManifest, &metadata) != nil || metadata.Version == "" {
		return "", errors.New("invalid frontend build-version metadata")
	}
	return frontendVersion(files, metadata.Version), nil
}

func frontendVersion(content fs.FS, fallback string) string {
	if raw, e := fs.ReadFile(content, "dist/version.txt"); e == nil {
		return systemversion.DecodeVersionFile(raw)
	}
	return fallback
}
