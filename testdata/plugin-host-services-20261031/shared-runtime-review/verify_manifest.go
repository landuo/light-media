// Read-only verification of publisher manifests using the NAS-paired wheel key.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	directory := "work/plugin-host-browser-20261101"
	version := "146.0.7680.177.5"
	if len(os.Args) == 3 {
		directory, version = os.Args[1], os.Args[2]
	}
	m, e := os.ReadFile(filepath.Join(directory, "SHA256SUMS"))
	if e != nil {
		panic(e)
	}
	b, e := os.ReadFile(filepath.Join(directory, "SHA256SUMS.sig"))
	if e != nil {
		panic(e)
	}
	sig, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if e != nil {
		panic(e)
	}
	pub, e := base64.StdEncoding.DecodeString("MKFKwIhUcKWq5xTuNA0Ovg99njcDEcEJvmWYYhApvaU=")
	if e != nil {
		panic(e)
	}
	valid := ed25519.Verify(pub, m, sig) && strings.HasPrefix(string(m), "version="+version+"\n")
	json.NewEncoder(os.Stdout).Encode(map[string]any{"ed25519_signature_and_version_valid": valid, "public_key_source": "NAS-paired cloakbrowser0.5.6 config.py:BINARY_SIGNING_PUBKEYS", "version": version, "manifest": string(m)})
	if !valid {
		os.Exit(1)
	}
}
