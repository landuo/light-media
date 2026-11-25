package autosignin

import (
	"encoding/json"
	"net/url"
	"os"
	"reflect"
	"testing"
)

func ocrDomainExpected(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	v, e := sourceRestoreSavedJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSourceOCRFullJSONValueDomain47(t *testing.T) {
	checkSourceOCRValueDomain(t, "source.json", 47)
}
func TestSourceOCRFullJSONMixedDepth9(t *testing.T) {
	checkSourceOCRValueDomain(t, "mixed-depth.json", 9)
}
func TestSourceOCRFullJSONEmptyDepth4(t *testing.T) {
	checkSourceOCRValueDomain(t, "empty-depth.json", 4)
}

func checkSourceOCRValueDomain(t *testing.T, filename string, count int) {
	raw, e := os.ReadFile("testdata/source-ocr-json-values/" + filename)
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Rows []struct {
			Name, Body, HelperType string `json:"-"`
			Name2                  string `json:"name"`
			Body2                  string `json:"body"`
			HelperType2            string `json:"helper_type"`
			Truth                  bool
			HelperError            *struct{ Type, Message string } `json:"helper_error"`
			Operations             map[string]struct {
				Value json.RawMessage
				Error *struct{ Type, Message string }
			}
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil || len(source.Rows) != count {
		t.Fatal(e, len(source.Rows))
	}
	for _, r := range source.Rows {
		t.Run(r.Name2, func(t *testing.T) {
			result, e := sourceOCRServiceResult([]byte(r.Body2), "application/json")
			if r.HelperError != nil {
				if e == nil || e.Error() != r.HelperError.Message {
					t.Fatal("source helper exception differs", e, r.HelperError)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			value := result
			if v, ok := result.(*sourceOCRValue); ok {
				value = v.value
			}
			if typeName(value) != r.HelperType2 || Truth(result) != r.Truth {
				t.Fatal("source type/truth differs", typeName(value), r.HelperType2, Truth(result), r.Truth)
			}
			display := pythonScalar(result)
			if want := ocrDomainExpected(t, r.Operations["str"].Value); display != want {
				t.Fatal("source str differs", display, want)
			}
			repr := sourceContainerRepr(value)
			if want := ocrDomainExpected(t, r.Operations["repr"].Value); repr != want {
				t.Fatal("source repr differs", repr, want)
			}
			length, e := sourceLength(result)
			if failure := r.Operations["len"].Error; failure != nil {
				if e == nil || e.Error() != failure.Message {
					t.Fatal("source len exception differs", e, failure)
				}
			} else {
				if e != nil || json.Number(string(r.Operations["len"].Value)) != json.Number(jsonNumber(length)) {
					t.Fatal("source len differs", length, e, string(r.Operations["len"].Value))
				}
			}
			form, e := sourceOCRFormValues(value)
			if failure := r.Operations["form"].Error; failure != nil {
				if e == nil || e.Error() != failure.Message {
					t.Fatal("source form exception differs", e, failure)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			values := url.Values{"imagestring": form}
			body := values.Encode()
			want := ocrDomainExpected(t, r.Operations["form"].Value)
			if !reflect.DeepEqual(body, want) {
				t.Fatal("actual Requests encoded form differs", body, want)
			}
		})
	}
}
func jsonNumber(v int) string { raw, _ := json.Marshal(v); return string(raw) }
