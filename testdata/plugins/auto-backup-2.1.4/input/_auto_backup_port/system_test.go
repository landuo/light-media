package autobackup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

type versionConnector struct{ row any }

func (c versionConnector) Connect(context.Context) (driver.Conn, error) {
	return &versionConn{c.row}, nil
}
func (c versionConnector) Driver() driver.Driver { return versionDriver{} }

type versionDriver struct{}

func (versionDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type versionConn struct{ row any }

func (c *versionConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *versionConn) Close() error                        { return nil }
func (c *versionConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *versionConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if query != "SHOW server_version;" {
		return nil, errors.New("changed original SQL")
	}
	if c.row == "__error__" {
		return nil, errors.New("owned query error")
	}
	return &versionRows{row: c.row}, nil
}

type versionRows struct {
	row  any
	done bool
}

func (r *versionRows) Columns() []string { return []string{"server_version"} }
func (r *versionRows) Close() error      { return nil }
func (r *versionRows) Next(values []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	if r.row == nil {
		return io.EOF
	}
	v, ok := r.row.([]any)
	if !ok || len(v) == 0 {
		return io.EOF
	}
	switch n := v[0].(type) {
	case nil:
		values[0] = nil
	case string:
		values[0] = n
	case json.Number:
		if v, e := n.Int64(); e == nil {
			values[0] = v
		} else {
			v, _ := n.Float64()
			values[0] = v
		}
	default:
		values[0] = text(n)
	}
	return nil
}
func TestInstalledSystemHelpersAndClientProvisionSourceOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/system-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct {
		Container []struct {
			Input  string
			Output *string
		}
		Versions []struct {
			Row    any
			Output int
		}
		Plans []struct {
			UID         int `json:"uid"`
			Docker      bool
			ContainerID *string `json:"container_id"`
			Proxy       string
			Plan        ClientProvisionRequest
		}
	}
	decode(t, raw, &f)
	if len(f.Container) != 10 || len(f.Plans) != 32 {
		t.Fatal("system source scope changed")
	}
	for _, c := range f.Container {
		if canonical(ContainerID(c.Input)) != canonical(c.Output) {
			t.Error("mountinfo source differs", c.Input, ContainerID(c.Input), c.Output)
		}
	}
	for _, c := range f.Plans {
		got := ClientProvisionPlan(17, RuntimeSnapshot{c.UID, c.Docker, c.ContainerID}, c.Proxy)
		if canonical(got) != canonical(c.Plan) {
			t.Error("provision source branch differs", got, c.Plan)
		}
	}
	for _, c := range f.Versions {
		db := sql.OpenDB(versionConnector{c.Row})
		got := MajorVersion(context.Background(), db)
		db.Close()
		if got != c.Output {
			t.Error("SHOW server_version source differs", c.Row, got, c.Output)
		}
	}
}
func TestDateSchedulerOriginalOnceMisfireAndStopWait(t *testing.T) {
	ctx := context.Background()
	executed := make(chan struct{}, 1)
	now := time.Now()
	s := NewDateScheduler(func() time.Time { return now }, func(context.Context) error { executed <- struct{}{}; return nil }, nil)
	if e := s.AddDate(ctx, now.Add(-2*time.Second), "自动备份"); e != nil {
		t.Fatal(e)
	}
	if e := s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for {
		n, _ := s.Jobs(ctx)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("misfire job was not evaluated")
		}
		time.Sleep(time.Millisecond)
	}
	if e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-executed:
		t.Fatal("expired date ran")
	default:
	}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	s = NewDateScheduler(time.Now, func(ctx context.Context) error {
		close(started)
		<-release
		if ctx.Err() != nil {
			t.Error("source shutdown canceled active backup")
		}
		return nil
	}, nil)
	if e := s.AddDate(ctx, time.Now(), "自动备份"); e != nil {
		t.Fatal(e)
	}
	if e := s.Start(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("date did not run")
	}
	go func() { _ = s.Clear(ctx); _ = s.Shutdown(ctx); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown did not wait for source job")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
	if s.Running() {
		t.Fatal("scheduler running after shutdown")
	}
	if n, e := s.Jobs(ctx); n != 0 || e != nil {
		t.Fatal(n, e)
	}
}

func TestSourceCronUsesLocalZoneAndOnceUsesSettingsZone(t *testing.T) {
	r := &rig{trace: []any{}}
	d := r.deps()
	d.LocalTimezone = "America/New_York"
	p := New(d)
	if e := p.Init(context.Background(), Object{"enabled": true, "onlyonce": true, "cron": "0 3 * * *"}); e != nil {
		t.Fatal(e)
	}
	spec, e := p.ServiceTrigger()
	if e != nil || spec == nil || spec.Timezone != "America/New_York" {
		t.Fatal("source cron local timezone lost", spec, e)
	}
	if canonical(r.trace[0]) != `["scheduler_new",{"timezone":"Asia/Shanghai"}]` {
		t.Fatal("source once no longer uses settings timezone", r.trace)
	}
}
