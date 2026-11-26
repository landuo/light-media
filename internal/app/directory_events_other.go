//go:build !linux

package app

import "context"

// The existing scanner handles platforms without Linux close-write events.
func (a *App) watchDirectoryEvents(context.Context)          {}
func directoryFileReady(_ string, allowUnverified bool) bool { return allowUnverified }
