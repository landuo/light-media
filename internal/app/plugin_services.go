package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// PluginServiceInfo is the public projection of a registered service. Its
// method input and credentials never leave the host. Registration is in-memory.
type PluginServiceInfo struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	ServiceID  string `json:"service_id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	NextRun    string `json:"next_run"`
	CanRun     bool   `json:"can_run"`
	due        time.Time
	busy       bool
}

// Match MoviePilot's TimerUtils.time_difference: floor seconds, omit zero
// units, and show seconds only when no larger unit is present.
func pluginServiceTimeDifference(due, at time.Time) string {
	seconds := int64(due.Sub(at) / time.Second)
	if seconds <= 0 {
		return ""
	}
	var out strings.Builder
	for _, unit := range []struct {
		seconds int64
		label   string
	}{{86400, "天"}, {3600, "小时"}, {60, "分钟"}} {
		if n := seconds / unit.seconds; n > 0 {
			fmt.Fprintf(&out, "%d%s", n, unit.label)
		}
		seconds %= unit.seconds
	}
	if out.Len() == 0 && seconds > 0 {
		fmt.Fprintf(&out, "%d秒", seconds)
	}
	return out.String()
}

// Called inside a.api, after its standard authentication and POST origin guard.
// Original MoviePilot's GET executor also receives the write-origin guard.
func (a *App) pluginServicesHTTP(w http.ResponseWriter, r *http.Request, path string) bool {
	compat := path
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		compat = path[3:]
	}
	if (path == "plugins/services" || compat == "dashboard/schedule") && r.Method == http.MethodGet {
		writeJSON(w, r, a.originalPublicServices(time.Now()))
		return true
	}
	instance, id := "", ""
	if path == "plugins/services/run" && r.Method == http.MethodPost {
		var input struct {
			InstanceID string `json:"instance_id"`
			ServiceID  string `json:"service_id"`
		}
		if err := decodeBody(w, r, &input); err != nil {
			a.writeError(w, r, err)
			return true
		}
		instance, id = input.InstanceID, input.ServiceID
	} else if compat == "system/runscheduler" && r.Method == http.MethodGet {
		if !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "禁止跨站写入请求", 403})
			return true
		}
		for _, item := range a.originalPublicServices(time.Now()) {
			if item.ID == r.URL.Query().Get("jobid") {
				instance, id = item.InstanceID, item.ServiceID
				break
			}
		}
	} else {
		return false
	}
	if err := a.runOriginalPublicService(r.Context(), instance, id); err != nil {
		a.writeError(w, r, err)
	} else {
		writeJSON(w, r, map[string]bool{"success": true, "accepted": true})
	}
	return true
}
