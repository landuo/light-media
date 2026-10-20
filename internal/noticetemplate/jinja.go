package noticetemplate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"light-media/internal/jinja"
	"light-media/internal/jinja/exec"
	"light-media/internal/jinja/loaders"
	"light-media/internal/jinja/nodes"
	"light-media/internal/jinja/parser"
)

// RenderWithContext is an experimental native dependency boundary. Fidelity is
// evaluated against the original Jinja 3.1.6 oracle before App integration.
func RenderWithContext(ctx context.Context, text string, variables Object) (output string, renderErr error) {
	defer func() {
		if recover() != nil {
			output, renderErr = "", fmt.Errorf("%w: native execution boundary", exec.ErrUnsupported)
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	loader, err := loaders.NewMemoryLoader(map[string]string{"/__inline__": text})
	if err != nil {
		return "", err
	}
	environment := *gonja.DefaultEnvironment
	environment.Context = gonja.DefaultContext.Inherit()
	// These Gonja extensions are absent from Jinja2.Template's default
	// environment. A source-invalid tag is a source syntax error. Loader
	// statements still need a native implementation of Jinja's no-loader
	// semantics (including unreachable branches); never render our inline
	// template recursively as a substitute.
	controls := make(map[string]parser.ControlStructureParser)
	for _, name := range []string{"autoescape", "block", "filter", "for", "if", "macro", "raw", "set", "with", "call"} {
		p, _ := gonja.DefaultEnvironment.ControlStructures.Get(name)
		controls[name] = p
	}
	for _, name := range []string{"do", "break", "continue", "trans"} {
		controls[name] = func(*parser.Parser, *parser.Parser) (nodes.ControlStructure, error) {
			return nil, &exec.SourceError{Class: "TemplateSyntaxError"}
		}
	}
	for _, name := range []string{"extends", "include", "import", "from"} {
		controls[name] = func(*parser.Parser, *parser.Parser) (nodes.ControlStructure, error) {
			return nil, fmt.Errorf("%w: loader statement", exec.ErrUnsupported)
		}
	}
	environment.ControlStructures = exec.NewControlStructureSet(controls)
	for _, name := range []string{"lipsum", "cycler"} {
		environment.Context.Set(name, func(*exec.Evaluator, *exec.VarArgs) (*exec.Value, error) {
			return nil, fmt.Errorf("%w: source global function", exec.ErrUnsupported)
		})
	}
	for _, name := range []string{"_", "gettext", "ngettext"} {
		environment.Context.Set(name, exec.AsUndefined())
	}
	tpl, err := exec.NewTemplate("/__inline__", gonja.DefaultConfig, loader, &environment)
	if err != nil {
		return "", err
	}
	out, err := tpl.ExecuteToString(exec.NewContext(normalizeForRender(variables).(map[string]any)))
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if err != nil {
		var source *exec.SourceError
		if !errors.As(err, &source) {
			return "", fmt.Errorf("%w: native engine error", exec.ErrUnsupported)
		}
	}
	return out, err
}
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			if n, e := strconv.ParseInt(string(x), 10, 64); e == nil {
				return n
			}
			if n, ok := new(big.Int).SetString(string(x), 10); ok {
				return n
			}
		}
		if n, e := strconv.ParseFloat(string(x), 64); e == nil {
			return n
		}
		return string(x)
	case *big.Int:
		if x.IsInt64() {
			return x.Int64()
		}
		return x
	case map[string]any:
		out := Object{}
		for k, v := range x {
			out[k] = normalizeNumbers(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeNumbers(v)
		}
		return out
	}
	return v
}

// Unbounded values remain intact in the source context/cache. If a template
// actually consumes one, return an explicit native dependency rather than a
// rounded float or a false Python TypeError. Unused source fields do not fail.
func normalizeForRender(value any) any {
	value = normalizeNumbers(value)
	switch v := value.(type) {
	case *big.Int:
		if !v.IsInt64() {
			return exec.ValueError(fmt.Errorf("%w: unbounded Jinja integer", exec.ErrUnsupported))
		}
		return v.Int64()
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeForRender(item)
		}
	case []any:
		for i, item := range v {
			v[i] = normalizeForRender(item)
		}
	case *exec.Dict:
		copy := exec.NewDict()
		for _, p := range v.Pairs {
			copy.Pairs = append(copy.Pairs, &exec.Pair{Key: p.Key, Value: exec.AsValue(normalizeForRender(p.Value.Interface()))})
		}
		return copy
	}
	return value
}
