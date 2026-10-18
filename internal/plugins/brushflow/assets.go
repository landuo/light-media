package brushflow

import (
	"embed"
	"io/fs"
)

//go:embed frontend/assets/*
var frontendFiles embed.FS

func FrontendAssets() fs.FS {
	f, e := fs.Sub(frontendFiles, "frontend")
	if e != nil {
		panic(e)
	}
	return f
}
