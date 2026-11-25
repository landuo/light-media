package sitestatistic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	Enabled, OnlyOnce, Dashboard, Notify any
	NotifyRaw                            json.RawMessage
}

func NewConfig() Config {
	return Config{Enabled: false, OnlyOnce: false, Dashboard: "today", Notify: ""}
}
func (c *Config) load(input any) error {
	if !truth(input) {
		return nil
	}
	fields, ok := input.(map[string]any)
	if !ok {
		return &SourceError{"AttributeError", fmt.Sprintf("'%s' object has no attribute 'get'", sourceTypeName(input))}
	}
	c.Enabled = fields["enabled"]
	c.OnlyOnce = fields["onlyonce"]
	c.Dashboard = fields["dashboard_type"]
	if !truth(c.Dashboard) {
		c.Dashboard = "today"
	}
	c.Notify = fields["notify_type"]
	c.NotifyRaw = nil
	if !truth(c.Notify) {
		c.Notify = ""
	}
	return nil
}
func privateTimezone() (*time.Location, error) {
	zone, exists := os.LookupEnv("TZ")
	if !exists {
		zone = "Asia/Shanghai"
	} else {
		zone = strings.TrimFunc(zone, sourcePythonSpace)
	}
	if zone == "" || strings.HasPrefix(zone, ":") || zone == "Local" {
		return nil, errors.New("invalid source private scheduler timezone")
	}
	return time.LoadLocation(zone)
}
func initialize(ctx context.Context, input any, c *Config, h Host, now func() time.Time, schedule func(time.Time) error, loaded func(Config)) error {
	if e := c.load(input); e != nil {
		return e
	}
	if loaded != nil {
		loaded(*c)
	}
	if !truth(c.OnlyOnce) {
		return nil
	}
	fields, ok := input.(map[string]any)
	if !ok {
		return &SourceError{"TypeError", fmt.Sprintf("'%s' object does not support item assignment", sourceTypeName(input))}
	}
	fields["onlyonce"] = false
	// This flag mutation precedes scheduler timezone validation in this source.
	location, e := privateTimezone()
	if e != nil {
		return e
	}
	if e = schedule(now().In(location).Add(3 * time.Second)); e != nil {
		return e
	}
	return h.SaveConfig(ctx, fields)
}
