package autosignin

import (
	"context"
	"errors"
	"testing"
)

type oracleAnswerCache struct {
	input Object
	steps *[]any
}

func (c oracleAnswerCache) Ensure(context.Context) error {
	*c.steps = append(*c.steps, Object{"op": "cache_ensure"})
	if Truth(c.input["ensure_error"]) {
		return errors.New(textValue(c.input["ensure_error"]))
	}
	if exists, ok := c.input["directory_exists"].(bool); ok && !exists {
		*c.steps = append(*c.steps, Object{"op": "cache_makedirs"})
	}
	return nil
}
func (c oracleAnswerCache) Read(context.Context) ([]byte, error) {
	*c.steps = append(*c.steps, Object{"op": "cache_read"})
	if Truth(c.input["read_error"]) {
		return nil, ErrTjuptCacheIO
	}
	if value, ok := c.input["cache"].(string); ok {
		return []byte(value), nil
	}
	return nil, ErrTjuptCacheIO
}
func (c oracleAnswerCache) Write(_ context.Context, data []byte) error {
	if Truth(c.input["write_error"]) {
		*c.steps = append(*c.steps, Object{"op": "cache_write_error"})
		return ErrTjuptCacheIO
	}
	*c.steps = append(*c.steps, Object{"op": "cache_write", "data": string(data)})
	return nil
}

type oraclePublicPages struct{ pages *fixedPages }

func (p oraclePublicPages) send(ctx context.Context, request PageRequest) (*PageResponse, error) {
	before := len(p.pages.calls)
	response, err := p.pages.Do(ctx, request)
	call := p.pages.calls[before].(Object)
	delete(call, "ua")
	delete(call, "cookies")
	return response, err
}
func (p oraclePublicPages) Suggest(ctx context.Context, label string) (*PageResponse, error) {
	return p.send(ctx, PageRequest{Method: "GET", URL: "https://movie.douban.com/j/subject_suggest?q=" + label, NoCookies: true})
}
func (p oraclePublicPages) Image(ctx context.Context, address string) (*PageResponse, error) {
	return p.send(ctx, PageRequest{Method: "GET", URL: address, NoCookies: true, Binary: true, Referer: "https://movie.douban.com"})
}
func TestActualInstalledTjuptWholeFlow(t *testing.T) {
	checkInstalledAdapterReference(t, "tjupt-reference.json", 131)
}
