package autobackup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DumpError contains a stable classification. Neither passwords nor raw dump,
// server stderr or connection parameters are included in public errors.
type DumpError struct{ Kind string }

func (e *DumpError) Error() string { return "PostgreSQL backup: " + e.Kind }

type PostgresOptions struct {
	// Binary is a host-selected, absolute pg_dump path, packaged into the image.
	Binary                                   string
	Host, Port, Username, Database, Password string
	// Env is trusted process baseline, never a worker-provided map.
	Env []string
	// ResolveClient is a trusted host capability for a packaged fixed client.
	// It is used only when the pinned binary is absent; no apt/shell is run.
	ResolveClient func(context.Context, ClientProvisionRequest) (string, error)
	VersionDB     *sql.DB
	Runtime       RuntimeSnapshot
	Proxy         string
}
type NativePostgres struct{ options PostgresOptions }

func NewPostgres(o PostgresOptions) (*NativePostgres, error) {
	if !filepath.IsAbs(o.Binary) || strings.ContainsAny(o.Binary, "\x00\r\n") {
		return nil, errors.New("unbound PostgreSQL client")
	}
	if o.Env != nil {
		o.Env = append([]string{}, o.Env...)
	}
	if o.Runtime.ContainerID != nil {
		id := *o.Runtime.ContainerID
		o.Runtime.ContainerID = &id
	}
	return &NativePostgres{o}, nil
}
func (d *NativePostgres) Dump(ctx context.Context, dest string) error {
	if !filepath.IsAbs(dest) {
		return &DumpError{Kind: "invalid_destination"}
	}
	o := d.options
	binary := o.Binary
	info, e := os.Stat(binary)
	if e != nil || info.IsDir() {
		if o.ResolveClient == nil {
			return &DumpError{Kind: "client_unavailable"}
		}
		binary, e = o.ResolveClient(ctx, ClientProvisionPlan(MajorVersion(ctx, o.VersionDB), o.Runtime, o.Proxy))
		if e != nil || !filepath.IsAbs(binary) || strings.ContainsAny(binary, "\x00\r\n") {
			return &DumpError{Kind: "client_unavailable"}
		}
		info, e = os.Stat(binary)
		if e != nil || info.IsDir() {
			return &DumpError{Kind: "client_unavailable"}
		}
	}
	cmd := exec.CommandContext(ctx, binary, "-h", o.Host, "-p", o.Port, "-U", o.Username, "-d", o.Database, "-f", dest)
	baseline := o.Env
	if baseline == nil {
		baseline = os.Environ()
	}
	for _, v := range baseline {
		if !strings.HasPrefix(v, "PGPASSWORD=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "PGPASSWORD="+o.Password)
	cmd.Stdout = &discardLimited{limit: 4096}
	cmd.Stderr = &discardLimited{limit: 4096}
	if e = cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return &DumpError{Kind: "canceled"}
		}
		return &DumpError{Kind: "dump_failed"}
	}
	return nil
}

type discardLimited struct {
	limit int
	b     bytes.Buffer
}

func (d *discardLimited) Write(p []byte) (int, error) {
	if d.limit > d.b.Len() {
		n := d.limit - d.b.Len()
		if n > len(p) {
			n = len(p)
		}
		_, _ = d.b.Write(p[:n])
	}
	return len(p), nil
}

// MajorVersion is the original SHOW server_version projection and default 17.
// The Session/transaction is supplied by the trusted host; no DSN crosses IPC.
func MajorVersion(ctx context.Context, db *sql.DB) int {
	if db == nil {
		return 17
	}
	var raw any
	if e := db.QueryRowContext(ctx, "SHOW server_version;").Scan(&raw); e != nil {
		return 17
	}
	v, ok := raw.(string)
	if !ok {
		return 17
	}
	parts := strings.Split(v, ".")
	n, e := integer(parts[0])
	if e != nil || !n.IsInt64() {
		return 17
	}
	return int(n.Int64())
}
