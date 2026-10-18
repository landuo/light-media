package cloudflarespeedtest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

// Native dependency admission belongs to the trusted App. These helpers do not
// turn worker JSON or a user button into artifact authority. They preserve the
// frozen core's file boundary while allowing the host to verify official bytes.
func MaterializeApprovedArchive(ctx context.Context, files *OwnedFiles, req InstallRequest, data []byte, pins Pins) error {
	if files == nil || !releaseURL(req) {
		return ErrArtifactUnverified
	}
	if err := pins.archive(req, data); err != nil {
		return err
	}
	return files.write(ctx, req.Archive, data, 0600)
}

// Current official Linux releases added one matching platform directory. The
// source plugin expects cfst/ip.txt at its data root. Normalize only that exact
// verified prefix, after authenticating the original official archive bytes.
func ExtractApprovedArchive(ctx context.Context, files *OwnedFiles, req InstallRequest, data []byte, pins Pins) error {
	if files == nil || !releaseURL(req) {
		return ErrArtifactUnverified
	}
	if e := pins.archive(req, data); e != nil {
		return e
	}
	if !strings.HasSuffix(req.Archive, ".tar.gz") {
		return extractArchive(ctx, files, req, data)
	}
	gz, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		return &ExtractionError{}
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	writerGZ := gzip.NewWriter(&out)
	tw := tar.NewWriter(writerGZ)
	prefix := strings.TrimSuffix(req.Archive, ".tar.gz") + "/"
	total := int64(0)
	count := 0
	for {
		header, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return &ExtractionError{}
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir && strings.TrimSuffix(name, "/") == strings.TrimSuffix(prefix, "/") {
			continue
		}
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || header.Size < 0 || header.Size > 128<<20 || total+header.Size > 128<<20 || count >= 256 {
			return ErrStorageScope
		}
		h := *header
		h.Name = name
		if e = tw.WriteHeader(&h); e != nil {
			return &ExtractionError{}
		}
		if _, e = io.CopyN(tw, tr, h.Size); e != nil {
			return &ExtractionError{}
		}
		total += h.Size
		count++
	}
	if e = tw.Close(); e != nil {
		return &ExtractionError{}
	}
	if e = writerGZ.Close(); e != nil {
		return &ExtractionError{}
	}
	return extractArchive(ctx, files, req, out.Bytes())
}

type ExecutableInspection struct{ SHA256, Module, GoVersion, VCSRevision, GOOS, GOARCH, CGOEnabled, VCSModified string }

func InspectOwnedExecutable(ctx context.Context, files *OwnedFiles, name string) (ExecutableInspection, error) {
	var out ExecutableInspection
	if files == nil {
		return out, ErrStorageScope
	}
	switch name {
	case "cfst", "cfst.exe", "CloudflareST", "CloudflareST.exe":
	default:
		return out, ErrStorageScope
	}
	data, err := files.read(ctx, name, 128<<20)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(data)
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "github.com/XIU2/CloudflareSpeedTest" || info.Main.Path != "github.com/XIU2/CloudflareSpeedTest" {
		return out, errors.New("official Cloudflare Go executable identity invalid")
	}
	out = ExecutableInspection{SHA256: hex.EncodeToString(sum[:]), Module: info.Path, GoVersion: info.GoVersion}
	for _, v := range info.Settings {
		switch v.Key {
		case "vcs.revision":
			out.VCSRevision = v.Value
		case "GOOS":
			out.GOOS = v.Value
		case "GOARCH":
			out.GOARCH = v.Value
		case "CGO_ENABLED":
			out.CGOEnabled = v.Value
		case "vcs.modified":
			out.VCSModified = v.Value
		}
	}
	return out, nil
}
