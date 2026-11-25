package encodingdetector

import (
	"bytes"
	"compress/zlib"
	"embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
)

//go:embed models/*.bin reference/generated/registry.json reference/generated/unicode-flags.bin.z reference/generated/python-aliases.json
var assets embed.FS

type EncodingInfo struct {
	Name      string   `json:"name"`
	Aliases   []string `json:"aliases"`
	Era       int      `json:"era"`
	Multibyte bool     `json:"multibyte"`
	Languages []string `json:"languages"`
}
type pairValue struct {
	A, B       string
	Categories map[byte][2]byte
}
type pythonCodec struct {
	Name string `json:"name"`
	Text bool   `json:"text"`
}
type metadata struct {
	PythonCodecs map[string]pythonCodec `json:"python_codecs"`
	Machines     []struct {
		Encoding string `json:"encoding"`
	} `json:"machines"`
	Registry    []EncodingInfo    `json:"registry"`
	Compat      map[string]string `json:"compat"`
	Aliases     map[string]string `json:"aliases"`
	Magic       [][2]string       `json:"magic"`
	Riff, Form  map[string]string
	ZipPrefixes [][2]string       `json:"zip_prefixes"`
	ZipSuffixes [][2]string       `json:"zip_suffixes"`
	Demotions   map[string][]byte `json:"demotions"`
	Koi8T       []byte            `json:"koi8t"`
}
type model struct {
	Language, Name, Key string
	Weights             []byte
	Norm                float64
}

var dataOnce sync.Once
var dataError error
var meta metadata
var byName map[string]EncodingInfo
var aliases map[string]string
var pythonAliases map[string]string
var pythonCodecLookup map[string]pythonCodec
var pythonAliasModules map[string]string
var models []model
var modelIndex map[string][]int
var idf, unicodeFlags []byte
var confusion []pairValue

func inflate(b []byte) ([]byte, error) {
	r, e := zlib.NewReader(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return io.ReadAll(r)
}
func loadData() error { dataOnce.Do(func() { dataError = loadAll() }); return dataError }
func loadAll() error {
	b, e := assets.ReadFile("reference/generated/registry.json")
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &meta); e != nil {
		return e
	}
	aBytes, aErr := assets.ReadFile("reference/generated/python-aliases.json")
	if aErr != nil {
		return aErr
	}
	if aErr = json.Unmarshal(aBytes, &pythonAliasModules); aErr != nil {
		return aErr
	}
	byName = map[string]EncodingInfo{}
	aliases = map[string]string{}
	pythonAliases = map[string]string{}
	pythonCodecLookup = map[string]pythonCodec{}
	for a, v := range meta.PythonCodecs {
		pythonCodecLookup[codecNormalize(a)] = v
	}
	for a, v := range meta.Aliases {
		pythonAliases[codecNormalize(a)] = v
	}
	for _, m := range meta.Machines {
		pythonAliases[codecNormalize(m.Encoding)] = m.Encoding
	}
	for _, name := range []string{"iso2022_jp", "iso2022_jp_1", "iso2022_jp_3"} {
		pythonAliases[codecNormalize(name)] = name
	}
	for _, v := range meta.Registry {
		byName[v.Name] = v
		aliases[strings.ToLower(v.Name)] = v.Name
		if pythonAliases[codecNormalize(v.Name)] == "" {
			pythonAliases[codecNormalize(v.Name)] = v.Name
		}
		for _, a := range v.Aliases {
			aliases[strings.ToLower(a)] = v.Name
		}
	}
	b, e = assets.ReadFile("models/idf.bin")
	if e != nil {
		return e
	}
	if len(b) != 65536 {
		return fmt.Errorf("invalid IDF size")
	}
	idf = b
	b, e = assets.ReadFile("reference/generated/unicode-flags.bin.z")
	if e != nil {
		return e
	}
	unicodeFlags, e = inflate(b)
	if e != nil {
		return e
	}
	if len(unicodeFlags) != 0x110000 {
		return fmt.Errorf("invalid unicode table")
	}
	b, e = assets.ReadFile("models/models.bin")
	if e != nil {
		return e
	}
	if len(b) < 8 || string(b[:4]) != "CMD2" {
		return fmt.Errorf("invalid model magic")
	}
	off := 8
	n := int(binary.BigEndian.Uint32(b[4:8]))
	if n > 10000 {
		return fmt.Errorf("invalid model count")
	}
	models = make([]model, n)
	for i := range models {
		if len(b) < off+4 {
			return io.ErrUnexpectedEOF
		}
		ln := int(binary.BigEndian.Uint32(b[off:]))
		off += 4
		if ln > 256 || len(b) < off+ln+8 {
			return io.ErrUnexpectedEOF
		}
		key := string(b[off : off+ln])
		off += ln
		norm := math.Float64frombits(binary.BigEndian.Uint64(b[off:]))
		off += 8
		p := strings.SplitN(key, "/", 2)
		if len(p) != 2 {
			return fmt.Errorf("invalid model name")
		}
		models[i] = model{Language: p[0], Name: p[1], Key: key, Norm: norm}
	}
	blob, e := inflate(b[off:])
	if e != nil {
		return e
	}
	if len(blob) != n*65536 {
		return fmt.Errorf("invalid model payload")
	}
	modelIndex = map[string][]int{}
	for i := range models {
		models[i].Weights = blob[i*65536 : (i+1)*65536]
		modelIndex[models[i].Name] = append(modelIndex[models[i].Name], i)
	}
	// Python preserves insertion order; aliases only fill absent canonical keys.
	for _, m := range models {
		canonical := lookup(m.Name)
		if canonical != "" && len(modelIndex[canonical]) == 0 {
			modelIndex[canonical] = modelIndex[m.Name]
		}
	}
	b, e = assets.ReadFile("models/confusion.bin")
	if e != nil {
		return e
	}
	if len(b) < 2 {
		return io.ErrUnexpectedEOF
	}
	count := int(binary.BigEndian.Uint16(b))
	off = 2
	take := func(n int) ([]byte, error) {
		if len(b) < off+n {
			return nil, io.ErrUnexpectedEOF
		}
		s := b[off : off+n]
		off += n
		return s, nil
	}
	for i := 0; i < count; i++ {
		x, e := take(1)
		if e != nil {
			return e
		}
		a, e := take(int(x[0]))
		if e != nil {
			return e
		}
		x, e = take(1)
		if e != nil {
			return e
		}
		bb, e := take(int(x[0]))
		if e != nil {
			return e
		}
		x, e = take(1)
		if e != nil {
			return e
		}
		p := pairValue{A: lookup(string(a)), B: lookup(string(bb)), Categories: map[byte][2]byte{}}
		for j := 0; j < int(x[0]); j++ {
			q, e := take(3)
			if e != nil {
				return e
			}
			p.Categories[q[0]] = [2]byte{q[1], q[2]}
		}
		confusion = append(confusion, p)
	}
	return nil
}
func codecNormalize(s string) string {
	var b strings.Builder
	punct := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' {
			if punct && b.Len() > 0 {
				b.WriteByte('_')
			}
			b.WriteByte(c)
			punct = false
		} else {
			punct = true
		}
	}
	return b.String()
}

func lookup(name string) string {
	if strings.IndexByte(name, 0) >= 0 {
		return ""
	}
	if v := aliases[strings.ToLower(name)]; v != "" {
		return v
	}
	s := codecNormalize(name)
	if v := meta.Aliases[s]; v != "" {
		if q := aliases[strings.ToLower(v)]; q != "" {
			return q
		}
		if v != name {
			return lookup(v)
		}
	}
	return ""
}
func Lookup(name string) (string, error) {
	if e := loadData(); e != nil {
		return "", e
	}
	v := lookup(name)
	if v == "" {
		return "", fmt.Errorf("unknown encoding %q", name)
	}
	return v, nil
}

type profile struct {
	Freq    []int64
	Nonzero []int
	Norm    float64
}

func newProfile(data []byte, filter map[byte][2]byte) *profile {
	p := &profile{}
	if len(data) < 2 {
		return p
	}
	p.Freq = make([]int64, 65536)
	var square int64
	for i := 0; i < len(data)-1; i++ {
		a, b := data[i], data[i+1]
		if filter != nil {
			_, x := filter[a]
			_, y := filter[b]
			if !x && !y {
				continue
			}
		}
		idx := int(a)<<8 | int(b)
		if p.Freq[idx] == 0 {
			p.Nonzero = append(p.Nonzero, idx)
		}
		p.Freq[idx] += int64(idf[idx])
	}
	for _, idx := range p.Nonzero {
		v := p.Freq[idx]
		square += v * v
	}
	p.Norm = math.Sqrt(float64(square))
	return p
}
func modelScore(p *profile, m model) float64 {
	if p.Norm == 0 || m.Norm == 0 {
		return 0
	}
	var dot int64
	for _, i := range p.Nonzero {
		dot += int64(m.Weights[i]) * p.Freq[i]
	}
	return float64(dot) / (m.Norm * p.Norm)
}
func bestLanguage(p *profile, enc string) (float64, string) {
	best := 0.
	lang := ""
	for _, i := range modelIndex[enc] {
		s := modelScore(p, models[i])
		if s > best {
			best = s
			lang = models[i].Language
		}
	}
	return best, lang
}
func statistical(data []byte, candidates []EncodingInfo) []Result {
	p := newProfile(data, nil)
	out := []Result{}
	for _, c := range candidates {
		s, l := bestLanguage(p, c.Name)
		if s > 0 {
			out = append(out, Result{Encoding: c.Name, Confidence: s, Language: l})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	return out
}
