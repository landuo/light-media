package autobackup

import (
	"os"
	"strings"
	"time"
	"unicode/utf8"

	py "light-media/internal/releasemeta/legacyanime"
)

// ContainerID is the exact consumed installed SystemHelper mountinfo projection.
// It accepts an already-authorized snapshot rather than opening a host file.
func ContainerID(data string) *string {
	runes := []rune(data)
	find := func(needle string) int {
		n := strings.Index(data, needle)
		if n < 0 {
			return -1
		}
		return utf8.RuneCountInString(data[:n])
	}
	rfind := func(delimiter rune, end int) int {
		if end < 0 {
			end += len(runes)
		}
		if end > len(runes) {
			end = len(runes)
		}
		for i := end - 1; i >= 0; i-- {
			if runes[i] == delimiter {
				return i
			}
		}
		return -1
	}
	slice := func(start, end int) string {
		if start < 0 {
			start += len(runes)
		}
		if end < 0 {
			end += len(runes)
		}
		if start < 0 {
			start = 0
		}
		if end < 0 {
			end = 0
		}
		if start > len(runes) {
			start = len(runes)
		}
		if end > len(runes) {
			end = len(runes)
		}
		if end < start {
			return ""
		}
		return string(runes[start:end])
	}
	var id string
	if i := find("resolv.conf"); i >= 0 {
		second := rfind('/', i)
		first := rfind('/', second) + 1
		id = slice(first, second)
		if utf8.RuneCountInString(id) < 20 {
			if i = find("/sys/fs/cgroup/devices"); i >= 0 {
				second = rfind(' ', i)
				first = rfind('/', second) + 1
				id = slice(first, second)
			}
		}
	}
	if id == "" {
		return nil
	}
	id = py.Strip(id)
	return &id
}

type RuntimeSnapshot struct {
	EffectiveUID int
	Docker       bool
	ContainerID  *string
}
type ClientProvisionRequest struct {
	MajorVersion    int               `json:"major_version"`
	Execution       string            `json:"execution"`
	ContainerID     *string           `json:"container_id"`
	Environment     map[string]string `json:"environment"`
	OriginalPackage string            `json:"original_package"`
}

// ClientProvisionPlan preserves source branch selection and proxy projection.
// The host maps this request to a packaged fixed client; it must never execute
// the original dynamic shell/apt script or mutate the host/container system.
func ClientProvisionPlan(major int, runtime RuntimeSnapshot, proxy string) ClientProvisionRequest {
	env := map[string]string{}
	if strings.HasPrefix(proxy, "http://") || strings.HasPrefix(proxy, "https://") {
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env[k] = proxy
		}
	}
	execution := "local"
	var id *string
	if runtime.EffectiveUID != 0 && runtime.Docker && runtime.ContainerID != nil && *runtime.ContainerID != "" {
		execution = "container"
		v := *runtime.ContainerID
		id = &v
	}
	return ClientProvisionRequest{major, execution, id, env, "postgresql-" + text(major)}
}

func localTimezoneName() string {
	values := []string{strings.TrimPrefix(os.Getenv("TZ"), ":"), time.Local.String()}
	if p, e := os.Readlink("/etc/localtime"); e == nil {
		if i := strings.LastIndex(p, "/zoneinfo/"); i >= 0 {
			values = append(values, p[i+10:])
		}
	}
	if b, e := os.ReadFile("/etc/timezone"); e == nil {
		values = append(values, strings.TrimSpace(string(b)))
	}
	for _, value := range values {
		if value == "" || value == "Local" {
			continue
		}
		if _, e := time.LoadLocation(value); e == nil {
			return value
		}
	}
	return "Etc/UTC"
}
