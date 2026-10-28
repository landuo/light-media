package app

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins/artifacts"
)

func originalPluginServerCPUArch(machine string) string {
	arch := strings.ToLower(machine)
	switch arch {
	case "amd64", "x86_64":
		return "x86_64"
	case "i386", "i686", "x86", "386", "x86_32":
		return "x86_32"
	case "aarch64", "arm64":
		return "Arm64"
	}
	if strings.HasPrefix(arch, "arm") || strings.HasPrefix(arch, "aarch") {
		return "Arm32"
	}
	return machine
}

// This is the pinned center-client compatibility identity, not light-media's
// application version. Match fixed settings.USER_AGENT/cpu_arch wire behavior.
func originalPluginServerIdentityAgent(system, release, machine string) string {
	return fmt.Sprintf("MoviePilot/%s (%s %s; %s)", strings.TrimPrefix(artifacts.MoviePilotCompatibilityVersion, "v"), system, release, originalPluginServerCPUArch(machine))
}

func originalPluginServerUserAgent() string {
	var uname unix.Utsname
	_ = unix.Uname(&uname)
	system, release, machine := []byte{}, []byte{}, []byte{}
	for _, value := range uname.Sysname {
		if value == 0 {
			break
		}
		system = append(system, byte(value))
	}
	for _, value := range uname.Release {
		if value == 0 {
			break
		}
		release = append(release, byte(value))
	}
	for _, value := range uname.Machine {
		if value == 0 {
			break
		}
		machine = append(machine, byte(value))
	}
	return originalPluginServerIdentityAgent(string(system), string(release), string(machine))
}
