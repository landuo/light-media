package ocr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"math"
	"os"
	"reflect"
	"strconv"
	"testing"
)

type observedResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body_b64"`
}
type observedCase struct {
	Name  string `json:"name"`
	Input struct {
		ImageURL    *string `json:"image_url"`
		ImageBase64 *string `json:"image_b64"`
		Cookie      *string `json:"cookie"`
		UserAgent   *string `json:"ua"`
	} `json:"input"`
	Get, Post  *observedResponse
	Value      plugins.OCRValue `json:"value"`
	ErrorClass string           `json:"error_class"`
	Calls      []struct {
		Method string `json:"method"`
		URL    string `json:"url"`
		JSON   struct {
			Base64 string `json:"base64_img"`
		} `json:"json"`
		Options struct {
			Cookies *string `json:"cookies"`
			UA      *string `json:"ua"`
		} `json:"options"`
	} `json:"calls"`
}

func sourceCases(t *testing.T, file string) []observedCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Cases []observedCase `json:"cases"`
	}
	if err = json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	if len(captured.Cases) == 0 {
		t.Fatal("empty source capture")
	}
	return captured.Cases
}
func sameValue(a, b plugins.OCRValue) bool {
	if a.Kind != b.Kind || a.Bool != b.Bool || !reflect.DeepEqual(a.Points, b.Points) || len(a.Items) != len(b.Items) || len(a.Entries) != len(b.Entries) {
		return false
	}
	if a.Kind == "float" {
		if a.Number == "nan" || a.Number == "+inf" || a.Number == "-inf" || b.Number == "nan" || b.Number == "+inf" || b.Number == "-inf" {
			if a.Number != b.Number {
				return false
			}
		} else {
			x, e := strconv.ParseFloat(a.Number, 64)
			y, f := strconv.ParseFloat(b.Number, 64)
			if e != nil || f != nil || x != y || math.Signbit(x) != math.Signbit(y) {
				return false
			}
		}
	} else if a.Number != b.Number {
		return false
	}
	for i := range a.Items {
		if !sameValue(a.Items[i], b.Items[i]) {
			return false
		}
	}
	for i := range a.Entries {
		if !reflect.DeepEqual(a.Entries[i].Key, b.Entries[i].Key) || !sameValue(a.Entries[i].Value, b.Entries[i].Value) {
			return false
		}
	}
	return true
}
func runSourceCases(t *testing.T, file string) {
	t.Helper()
	for _, sample := range sourceCases(t, file) {
		t.Run(sample.Name, func(t *testing.T) {
			calls := []Request{}
			c := Client{Endpoint: "https://owned.invalid/captcha/base64", Transport: func(ctx context.Context, request Request) (*Response, error) {
				calls = append(calls, request)
				r := sample.Post
				if request.Method == "GET" {
					r = sample.Get
				}
				if r == nil {
					return nil, nil
				}
				body, err := base64.StdEncoding.DecodeString(r.Body)
				if err != nil {
					t.Fatal(err)
				}
				return &Response{Status: r.Status, Content: body, ContentType: "application/json"}, nil
			}}
			got, err := c.GetCaptchaText(context.Background(), Input{ImageURL: sample.Input.ImageURL, ImageBase64: sample.Input.ImageBase64, Cookie: sample.Input.Cookie, UserAgent: sample.Input.UserAgent})
			if sample.ErrorClass != "" {
				var source *SourceError
				if !errors.As(err, &source) || source.Class != sample.ErrorClass {
					t.Fatalf("source error %s got %v", sample.ErrorClass, err)
				}
			} else if err != nil || !sameValue(got, sample.Value) {
				t.Fatalf("got %#v %v want %#v", got, err, sample.Value)
			}
			if len(calls) != len(sample.Calls) {
				t.Fatalf("calls %d want %d", len(calls), len(sample.Calls))
			}
			for i, request := range calls {
				wanted := sample.Calls[i]
				if request.Method != wanted.Method || request.URL != wanted.URL {
					t.Fatalf("request %d %#v", i, request)
				}
				if request.Method == "GET" {
					if !reflect.DeepEqual(request.Cookie, wanted.Options.Cookies) || !reflect.DeepEqual(request.UserAgent, wanted.Options.UA) {
						t.Fatal("image input cookies/ua differ")
					}
				} else if request.Base64Image != wanted.JSON.Base64 || request.Cookie != nil || request.UserAgent != nil {
					t.Fatal("POST payload or image credential separation differs")
				}
			}
		})
	}
}
func TestOrdinaryOCRFixedSourceObservations(t *testing.T) { runSourceCases(t, "helper-source.json") }
func TestOrdinaryOCRSourceStatusTruthiness(t *testing.T) {
	runSourceCases(t, "status-truthiness-source.json")
}
func TestOrdinaryOCRTransportErrorsAndCancel(t *testing.T) {
	b64 := "QQ"
	sentinel := errors.New("host revocation")
	c := Client{Endpoint: "owned", Transport: func(context.Context, Request) (*Response, error) { return nil, sentinel }}
	_, err := c.GetCaptchaText(context.Background(), Input{ImageBase64: &b64})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Transport = func(context.Context, Request) (*Response, error) {
		t.Fatal("canceled reached transport")
		return nil, nil
	}
	_, err = c.GetCaptchaText(ctx, Input{ImageBase64: &b64})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
