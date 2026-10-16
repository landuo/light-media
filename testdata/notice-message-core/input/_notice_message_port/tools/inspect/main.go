package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	f, e := os.Open("reference/installed-oracle.json")
	if e != nil {
		panic(e)
	}
	defer f.Close()
	var data struct {
		Envelopes []struct {
			Name   string
			Input  map[string]any
			Value  map[string]any
			Errors any
		}
		Periods []any
		Routes  []any
		Monitor []any
	}
	d := json.NewDecoder(f)
	d.UseNumber()
	if e = d.Decode(&data); e != nil {
		panic(e)
	}
	for _, c := range data.Envelopes {
		if !strings.HasPrefix(c.Name, "action-") && !strings.HasPrefix(c.Name, "userid-") && !strings.HasPrefix(c.Name, "force_reply-") {
			continue
		}
		out := map[string]any{"name": c.Name, "errors": c.Errors}
		for k := range c.Input {
			if c.Value != nil {
				out["value"] = c.Value[k]
			}
		}
		raw, _ := json.Marshal(out)
		fmt.Println(string(raw))
	}
	fmt.Printf("envelopes=%d periods=%d routes=%d monitor=%d\n", len(data.Envelopes), len(data.Periods), len(data.Routes), len(data.Monitor))
}
