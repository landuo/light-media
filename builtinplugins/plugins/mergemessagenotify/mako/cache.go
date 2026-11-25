package mako

import "strings"

func (p *templateParser) compileFlag(t templateToken, key string) (bool, error) {
	raw, exists := t.attrs[key]
	if !exists {
		return false, nil
	}
	expr, err := parseExpression(raw)
	if err != nil {
		return false, err
	}
	value, err := expr.eval(p.compile)
	return truth(value), err
}

func (p *templateParser) readCacheOptions(t templateToken) (bool, error) {
	var timeout expression
	for _, key := range t.attrOrder {
		if !strings.HasPrefix(key, "cache_") {
			continue
		}
		expr, err := namespaceAttributeExpression(t.attrs[key])
		if err != nil {
			return false, err
		}
		if key == "cache_timeout" {
			timeout = expr
		}
	}
	cached, err := p.compileFlag(t, "cached")
	if err != nil {
		return false, err
	}
	if t.text == "page" {
		p.pageCached, p.pageCacheTimeout = cached, timeout
	} else if cached {
		if timeout == nil {
			p.usesPageCacheTimeout = true
		} else if err := p.validateCacheTimeout(timeout); err != nil {
			return false, err
		}
	}
	return cached, nil
}

func (p *templateParser) validateCacheTimeout(timeout expression) error {
	if timeout == nil {
		return nil
	}
	value, err := timeout.eval(p.compile)
	if err != nil {
		return err
	}
	_, err = builtins["int"].(callable)(p.compile, []any{value}, nil)
	return err
}

// The fixed TemplateUtil uses Mako's default Beaker cache backend. The
// installed plugin environment has no Beaker, so accessing the cache raises
// before the key/options or the original body are evaluated.
func unavailableTemplateCache() error { return fault("RuntimeException") }
