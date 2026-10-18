package seedrescue

import (
	"embed"
	"io/fs"
)

// The original Vue federation graph is preserved verbatim. The host must supply
// its authenticated plugin API and shared Vue/Vuetify import scope. This package
// does not publish a second, unauthenticated HTTP server.
//
//go:embed frontend/assets/*
var frontend embed.FS

func FrontendAssets() fs.FS {
	assets, err := fs.Sub(frontend, "frontend")
	if err != nil {
		panic(err)
	}
	return assets
}
func RenderMode() (string, string) { return "vue", "dist/assets" }
func Commands() []Object           { return []Object{} }
