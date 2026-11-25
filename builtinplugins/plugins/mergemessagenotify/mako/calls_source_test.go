package mako

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSourceTemplateCalls141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-calls-1.4.1.json")
}

func TestSourceCallBoundaries141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-call-boundaries-1.4.1.json")
}

func TestSourceDecoratorCache141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-decorator-cache-1.4.1.json")
}

func TestSourceDefaultsAndNames141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-defaults-and-names-1.4.1.json")
}

func TestSourceDefinitionContext141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-definition-context-1.4.1.json")
}

func TestSourceNamedBlock141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-named-block-1.4.1.json")
}

func TestSourceNamedBlockEdges141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-named-block-edges-1.4.1.json")
}

func TestSourceMultiplePages141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-multiple-pages-1.4.1.json")
}

func TestSourcePageVariadic141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-page-variadic-1.4.1.json")
}

func TestSourceExternalTemplate141(t *testing.T) {
	runSourceCallFixture(t, "testdata/source-external-template-1.4.1.json")
}

func runSourceCallFixture(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var fixture struct {
		Cases []struct {
			Name, Template, Expected string
			Vars                     map[string]any
		}
	}
	dec := json.NewDecoder(file)
	dec.UseNumber()
	if err := dec.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			got, err := Render(t.Context(), row.Template, row.Vars)
			if err != nil || got != row.Expected {
				t.Fatalf("Render = %q, %v; want %q", got, err, row.Expected)
			}
		})
	}
}
