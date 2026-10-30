package app

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"light-media/internal/plugins"
)

// PluginServiceInfo is the public projection of a registered service. Its
// method input and credentials never leave the host. Registration is in-memory.
type PluginServiceInfo struct {
	ID               string         `json:"id"`
	InstanceID       string         `json:"instance_id"`
	ServiceID        string         `json:"service_id"`
	Name             string         `json:"name"`
	Provider         string         `json:"provider"`
	Status           string         `json:"status"`
	NextRun          *string        `json:"next_run"`
	CanRun           bool           `json:"can_run"`
	Progress         float64        `json:"progress"`
	ProgressText     *string        `json:"progress_text"`
	ProgressEnable   bool           `json:"progress_enable"`
	ProgressDetail   map[string]any `json:"progress_detail"`
	NameI18N         *string        `json:"name_i18n"`
	ProviderI18N     *string        `json:"provider_i18n"`
	StatusI18N       *string        `json:"status_i18n"`
	NextRunI18N      *string        `json:"next_run_i18n"`
	ProgressTextI18N *string        `json:"progress_text_i18n"`

	due               time.Time
	busy              bool
	registrationOrder uint64
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
	if route, ok := originalServiceWirePath(path); ok {
		a.originalServiceWireHTTP(w, r, route)
		return true
	}
	compat := path
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		compat = path[3:]
	}
	if jobID, ok := originalServiceProgressPath(compat); ok && r.Method == http.MethodGet {
		progress := a.originalServiceProgressSnapshot(jobID)
		if err := normalizeOriginalServiceProgressProjection(progress); err != nil {
			a.writeError(w, r, err)
			return true
		}
		if progress == nil {
			writeJSON(w, r, map[string]any{"success": false, "message": "后台服务不存在"})
		} else {
			writeJSON(w, r, map[string]any{"success": true, "data": progress})
		}
		return true
	}
	if (path == "plugins/services" || compat == "dashboard/schedule") && r.Method == http.MethodGet {
		items, err := a.originalPublicServicesResult(time.Now())
		if err != nil {
			a.writeError(w, r, err)
		} else {
			writeJSON(w, r, items)
		}
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
		if !r.URL.Query().Has("jobid") {
			a.writeError(w, r, &APIError{"VALIDATION_ERROR", "缺少 jobid 参数", http.StatusUnprocessableEntity})
			return true
		}
		jobID := plugins.LastQuery(r.URL.Query(), "jobid")
		if jobID == "" {
			writeJSON(w, r, map[string]any{"success": false, "message": "命令不能为空！"})
			return true
		}
		state := &a.pluginOriginalDirectJobs
		state.mu.Lock()
		for key, job := range state.jobs {
			if originalServiceID(job) == jobID {
				instance, id = key.instance, key.service
				break
			}
		}
		state.mu.Unlock()
		// Scheduler.start silently returns for missing/running jobs; this
		// original API acknowledges the request, not the eventual result.
		_ = a.runOriginalService(r.Context(), instance, id, true)
		writeJSON(w, r, map[string]bool{"success": true})
		return true
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
