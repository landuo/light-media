package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSystemProcessLinuxOwnedApp(t *testing.T) {
	worker := os.Getenv("SOURCE_SYSTEM_PROCESS_WORKER")
	if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" || worker == "" {
		t.Skip("requires the SystemProcess executable in the owned Linux ARM64 fixture")
	}
	// This short-lived process owns its kernel name. Its PID and name are
	// measured independently of the plugin's sampling library.
	pid, childName := systemProcessOwnedChild(t)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, worker)
	const id = "SystemProcess"
	const name = "系统进程"
	if manifest.PluginID != id || manifest.Version != "1.1" || manifest.Name != name || !manifest.HasPage {
		t.Fatal("unexpected actual SystemProcess worker")
	}
	path := "/api/plugins/" + id
	read := func(path string, out any) {
		t.Helper()
		response := callTest(t, a.Handler(), http.MethodGet, path, "", cookie, "")
		var envelope struct{ Data json.RawMessage }
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data) == 0 {
			t.Fatalf("GET %s returned HTTP %d without the source view", path, response.Code)
		}
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			t.Fatalf("GET %s view shape: %v", path, err)
		}
	}
	readForm := func(size, refresh, enabled string) {
		t.Helper()
		var form struct {
			RenderMode string `json:"render_mode"`
			Conf       []systemProcessOwnedNode
			Model      map[string]json.RawMessage
		}
		read(path+"/form", &form)
		if form.RenderMode != "vuetify" || len(form.Conf) != 1 || form.Conf[0].Component != "VForm" ||
			string(form.Model["dashboard_widget_size"]) != size || string(form.Model["dashboard_widget_refresh"]) != refresh ||
			string(form.Model["enable"]) != enabled {
			t.Fatal("native form or saved configuration differs from the source")
		}
	}
	readForm("8", "5", "")

	// Source get_page remains available when get_state is false. Discovery of
	// the dashboard is separately gated by enable.
	type dashboardMetadata struct{ ID, Name, Key string }
	var metadata []dashboardMetadata
	read("/api/plugins/dashboard/meta", &metadata)
	if len(metadata) != 0 {
		t.Fatal("disabled SystemProcess was listed in dashboard metadata")
	}
	var page struct {
		RenderMode string `json:"render_mode"`
		Page       []systemProcessOwnedNode
	}
	read(path+"/page", &page)
	if page.RenderMode != "vuetify" {
		t.Fatal("disabled detail did not return its native source render mode")
	}
	detailRows := assertSystemProcessOwnedTable(t, page.Page, pid, childName, "")

	save := callTest(t, a.Handler(), http.MethodPut, path,
		`{"enable":true,"dashboard_widget_size":6,"dashboard_widget_refresh":9}`, cookie, "")
	assertOriginalRegistrySuccess(t, save.Code, save.Body.Bytes())
	readForm("6", "9", "true")
	read("/api/plugins/dashboard/meta", &metadata)
	if len(metadata) != 1 || metadata[0] != (dashboardMetadata{ID: id, Name: name}) {
		t.Fatal("enabled SystemProcess dashboard metadata differs from the source")
	}
	readDashboard := func(instance, title string) int {
		t.Helper()
		var dashboard struct {
			ID, Name, Key string
			RenderMode    string `json:"render_mode"`
			Cols          map[string]int
			Attrs         struct {
				Title   string
				Refresh int
			}
			Elements []systemProcessOwnedNode
		}
		read("/api/plugins/"+instance+"/dashboard", &dashboard)
		if dashboard.ID != instance || dashboard.Name != title || dashboard.Key != "" ||
			dashboard.RenderMode != "vuetify" || dashboard.Attrs.Title != title || dashboard.Attrs.Refresh != 9 {
			t.Fatal("dashboard response did not retain its instance title and configured refresh")
		}
		wantCols := map[string]int{"cols": 12, "xxl": 6, "xl": 6, "lg": 6, "md": 6, "sm": 12, "xs": 12}
		if len(dashboard.Cols) != len(wantCols) {
			t.Fatal("dashboard column count differs from the source")
		}
		for col, want := range wantCols {
			if dashboard.Cols[col] != want {
				t.Fatalf("dashboard column %s differs from the source", col)
			}
		}
		return assertSystemProcessOwnedTable(t, dashboard.Elements, pid, childName, "242px")
	}
	dashboardRows := readDashboard(id, name)

	const cloneTitle = "Owned process panel"
	clone := callTest(t, a.Handler(), http.MethodPost, path+"/clone", `{"suffix":"Owned","name":"Owned process panel"}`, cookie, "")
	assertOriginalRegistrySuccess(t, clone.Code, clone.Body.Bytes())
	// Source direct get_dashboard reads the loaded object without the
	// discovery state gate, so the disabled clone exposes its own title.
	cloneRows := readDashboard(id+"owned", cloneTitle)
	t.Logf("actual SystemProcess 1.1 Linux ARM64 host: owned PID %d present, descending detail/dashboard/clone rows %d/%d/%d, config 6 columns/9 seconds and clone title verified",
		pid, detailRows, dashboardRows, cloneRows)
}

type systemProcessOwnedNode struct {
	Component string
	Props     map[string]json.RawMessage
	Text      json.RawMessage
	Content   []systemProcessOwnedNode
}

func assertSystemProcessOwnedTable(t *testing.T, nodes []systemProcessOwnedNode, ownedPID int, ownedName, maxHeight string) int {
	t.Helper()
	if len(nodes) != 1 || nodes[0].Component != "VTable" || len(nodes[0].Content) != 2 {
		t.Fatal("missing source process VTable")
	}
	table := nodes[0]
	var style map[string]string
	if json.Unmarshal(table.Props["style"], &style) != nil || style["max-height"] != maxHeight {
		t.Fatal("process table height differs from the source")
	}
	head, body := table.Content[0], table.Content[1]
	headers := []string{"进程ID", "进程名称", "CPU占用", "内存占用", "运行时长"}
	if head.Component != "thead" || body.Component != "tbody" || len(head.Content) != len(headers) || len(body.Content) < 2 {
		t.Fatal("process table headers or live rows are missing")
	}
	for i, title := range headers {
		var text string
		if head.Content[i].Component != "th" || json.Unmarshal(head.Content[i].Text, &text) != nil || text != title {
			t.Fatalf("process table header %d differs from the source", i)
		}
	}
	found := false
	previous := 0
	for _, row := range body.Content {
		var pid int
		cells := row.Content
		if row.Component != "tr" || len(cells) != len(headers) || json.Unmarshal(cells[0].Text, &pid) != nil ||
			pid <= 0 || previous != 0 && pid >= previous {
			t.Fatal("process table must keep numeric PIDs in strictly descending order")
		}
		previous = pid
		if pid != ownedPID {
			continue
		}
		var name, cpu, memory, elapsed string
		if json.Unmarshal(cells[1].Text, &name) != nil || name != ownedName ||
			json.Unmarshal(cells[2].Text, &cpu) != nil || !strings.HasSuffix(cpu, "%") ||
			json.Unmarshal(cells[3].Text, &memory) != nil || !strings.HasSuffix(memory, "MB") ||
			json.Unmarshal(cells[4].Text, &elapsed) != nil || !strings.HasSuffix(elapsed, "秒") {
			t.Fatal("owned process row name or display units differ from the source")
		}
		found = true
	}
	if !found {
		t.Fatal("actual process table omitted the owned live process")
	}
	return len(body.Content)
}

func systemProcessOwnedChild(t *testing.T) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	name := fmt.Sprintf("lmsp-%d", os.Getpid())
	child := exec.CommandContext(ctx, "/bin/sh", "-c", `printf '%s' "$1" > "/proc/$$/comm" || exit; printf 'ready\n'; read -r ignored || :`, "owned", name)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close() })
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = child.Wait() })
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(output).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatal("owned child did not set its kernel process name")
		}
	case <-ctx.Done():
		t.Fatal("owned child did not become ready")
	}
	pid := child.Process.Pid
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil || strings.TrimSpace(string(comm)) != name {
		t.Fatal("owned child kernel identity was not observable")
	}
	return pid, name
}
