package artifacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
)

// This names the pinned MoviePilot plugin compatibility surface, never the
// independent light-media application version. Go SDK/platform checks remain
// separate and cannot be granted by this author-supplied metadata.
const MoviePilotCompatibilityVersion = "v2.15.7"

var sourceSpecifier = regexp.MustCompile(`^(===|~=|==|!=|<=|>=|<|>)\s*([^\s;)]*)$`)
var sourceWildcard = regexp.MustCompile(`(?i)^v?(?:[0-9]+!)?[0-9]+(?:\.[0-9]+)*\.\*$`)
var sourcePostSuffix = regexp.MustCompile(`\.post[0-9]+(?:\.dev[0-9]+)?$`)

// CheckSystemVersion follows fixed PluginHelper.check_plugin_system_version:
// missing/None/empty bypass version parsing; malformed declarations fail;
// packaging.SpecifierSet.contains always includes prereleases here.
func CheckSystemVersion(raw json.RawMessage, host string) (bool, string) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return true, ""
	}
	var constraint string
	if json.Unmarshal(value, &constraint) != nil {
		return false, "插件限定的系统版本范围 system_version 必须是字符串，请使用 pip 依赖版本格式，例如 >=2.12.0,<3"
	}
	if constraint == "" {
		return true, ""
	}
	current, err := pep440.Parse(host)
	if err != nil {
		return false, fmt.Sprintf("当前 MoviePilot 版本 %s 无法解析，已拒绝安装带版本限制的插件", host)
	}
	invalid := func() (bool, string) {
		return false, fmt.Sprintf("插件限定的系统版本范围格式不正确：%s，请使用 pip 依赖版本格式，例如 >=2.12.0,<3", constraint)
	}
	compatible := true
	for _, part := range strings.Split(constraint, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		match := sourceSpecifier.FindStringSubmatch(part)
		if match == nil {
			return invalid()
		}
		if match[1] == "===" {
			compatible = compatible && strings.EqualFold(current.String(), match[2])
			continue
		}
		// The library deliberately accepts OR, missing/single '=' operators and
		// wildcard shortcuts. Per-component strict syntax above excludes them.
		if match[2] == "" || strings.Contains(match[2], "*") && !sourceWildcard.MatchString(match[2]) {
			return invalid()
		}
		spec, err := pep440.NewSpecifiers(part, pep440.WithPreRelease(true))
		if err != nil {
			return invalid()
		}
		matches := spec.Check(current)
		// packaging26.2's ~= prefix is built from the original specifier;
		// unlike the lower bound it retains a leading v and does not match
		// the normalized prospective version. Preserve this source behavior.
		if match[1] == "~=" && strings.HasPrefix(strings.ToLower(match[2]), "v") {
			matches = false
		}
		// packaging26.2's strict bounds exclude a pre/post-release of the
		// specified version, not all releases sharing its numeric base. The
		// generic library's WithPreRelease bypasses this distinction.
		if match[1] == "<" || match[1] == ">" {
			prospective, _ := pep440.Parse(current.Public())
			specified, _ := pep440.Parse(match[2])
			if match[1] == "<" {
				matches = prospective.LessThan(specified)
				if matches && !specified.IsPreRelease() && prospective.IsPreRelease() {
					earliest, err := pep440.Parse(specified.Public() + ".dev0")
					if err != nil {
						return invalid()
					}
					matches = prospective.LessThan(earliest)
				}
			} else {
				matches = prospective.GreaterThan(specified)
				if matches && !specified.IsPostRelease() && prospective.IsPostRelease() {
					base, err := pep440.Parse(sourcePostSuffix.ReplaceAllString(prospective.Public(), ""))
					if err != nil {
						return invalid()
					}
					matches = !base.Equal(specified)
				}
			}
		}
		compatible = matches && compatible
	}
	if compatible {
		return true, ""
	}
	return false, fmt.Sprintf("插件要求 MoviePilot 版本 %s，当前版本 %s 不满足，已拒绝安装", constraint, host)
}
func annotateDiscoverySystemVersion(value *Discovery, raw json.RawMessage) {
	compatible, message := CheckSystemVersion(raw, MoviePilotCompatibilityVersion)
	value.SystemVersion = append(json.RawMessage(nil), raw...)
	value.SystemVersionCompatible = &compatible
	value.SystemVersionMessage = message
	if !compatible {
		value.UnavailableReason = message
	}
}
