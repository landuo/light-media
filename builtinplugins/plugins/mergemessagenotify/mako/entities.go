package mako

import (
	_ "embed"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

//go:embed html4-entities.json
var entitiesJSON []byte
var entities map[rune]string

var loadEntities = sync.OnceFunc(func() {
	var data map[string]string
	if err := json.Unmarshal(entitiesJSON, &data); err != nil {
		panic(err)
	}
	entities = map[rune]string{}
	for k, v := range data {
		n, err := strconv.Atoi(k)
		if err != nil {
			panic(err)
		}
		entities[rune(n)] = v
	}
})

func escapeEntities(s string) string {
	loadEntities()
	var b strings.Builder
	for _, r := range s {
		if name, ok := entities[r]; ok {
			b.WriteString("&" + name + ";")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
