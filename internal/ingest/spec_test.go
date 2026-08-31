package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// TestSpecCoversEveryField fails when a field is added to the wire format
// without documenting it. GET /spec is the whole contract for stacks that have
// no SDK, so an undocumented field is a field nobody can send.
func TestSpecCoversEveryField(t *testing.T) {
	fields := SpecFields()
	if len(fields) < 40 {
		t.Fatalf("SpecFields returned %d fields, expected the full flat format", len(fields))
	}
	for _, f := range fields {
		if strings.TrimSpace(f.Doc) == "" {
			t.Errorf("%s: missing a `doc` struct tag", f.Name)
		}
		if f.Type == "" || f.Type == "invalid" {
			t.Errorf("%s: wireType could not classify the Go type", f.Name)
		}
	}

	// Every string field must declare a length cap or an explicit exemption:
	// an uncapped string reaches the WAL, whose 16-bit length prefix mis-frames
	// the segment above 64KB and corrupts every entry written after it.
	ft := reflect.TypeOf(flatEntry{})
	for i := 0; i < ft.NumField(); i++ {
		f := ft.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if fieldCaps[name] == 0 && !uncappedStrings[name] {
			t.Errorf("%s: string field with no fieldCaps entry and no uncappedStrings exemption", name)
		}
	}
}

// TestFieldCapsMatchIngest pushes an oversized value through the real mapper for
// every capped field and checks the truncation the spec advertises is the
// truncation that happens. Without this the published caps are a comment.
func TestFieldCapsMatchIngest(t *testing.T) {
	h := &Handler{}
	oversized := strings.Repeat("a", 100_000)

	for name, want := range fieldCaps {
		t.Run(name, func(t *testing.T) {
			fe := flatEntry{Kind: kindRequest} // so RequestSummary-only fields are built
			if !setStringField(&fe, name, oversized) {
				t.Fatalf("no flatEntry field with json tag %q", name)
			}
			got := truncatedLengths(h.flatToLogEntry(fe, time.Now().UTC()))
			if len(got) == 0 {
				t.Fatalf("declared a %d-byte cap but the value reached storage untruncated", want)
			}
			for _, n := range got {
				if n != want {
					t.Errorf("truncated to %d bytes, spec says %d", n, want)
				}
			}
		})
	}
}

// setStringField assigns v to the flatEntry field carrying the given json tag.
func setStringField(fe *flatEntry, jsonName, v string) bool {
	rv := reflect.ValueOf(fe).Elem()
	for i := 0; i < rv.NumField(); i++ {
		if strings.Split(rv.Type().Field(i).Tag.Get("json"), ",")[0] != jsonName {
			continue
		}
		if rv.Field(i).Kind() != reflect.String {
			return false
		}
		rv.Field(i).SetString(v)
		return true
	}
	return false
}

// truncatedLengths returns the byte length of every stored string that carries
// the truncation marker, across the entry and its request summary.
func truncatedLengths(entry store.LogEntry) []int {
	var out []int
	scan := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Kind() != reflect.String {
				continue
			}
			if s := v.Field(i).String(); strings.HasSuffix(s, truncationMarker) {
				out = append(out, len(s))
			}
		}
	}
	scan(reflect.ValueOf(entry))
	if entry.RequestSummary != nil {
		scan(reflect.ValueOf(*entry.RequestSummary))
	}
	return out
}

func TestHandleSpec_Markdown(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleSpec(rec, httptest.NewRequest("GET", "/spec", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("content-type: want markdown, got %q", ct)
	}
	body := rec.Body.String()
	for _, f := range SpecFields() {
		if !strings.Contains(body, "`"+f.Name+"`") {
			t.Errorf("spec does not document %q", f.Name)
		}
	}
	for _, want := range []string{"?validate=1", "X-Batch-ID", "Never block", "kind=request"} {
		if !strings.Contains(body, want) {
			t.Errorf("spec is missing the %q guidance", want)
		}
	}
	// The examples must be copy-pasteable against the server that served them.
	if !strings.Contains(body, "http://example.com/api/v2/logs") {
		t.Errorf("spec did not use the request's own origin in its examples")
	}
}

func TestHandleSpec_JSON(t *testing.T) {
	rec := httptest.NewRecorder()
	HandleSpec(rec, httptest.NewRequest("GET", "/spec?format=json", nil))

	var got struct {
		Endpoint string      `json:"endpoint"`
		Fields   []FieldSpec `json:"fields"`
		Levels   []string    `json:"levels"`
		Example  []flatEntry `json:"example"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if len(got.Fields) != len(SpecFields()) {
		t.Errorf("fields: want %d, got %d", len(SpecFields()), len(got.Fields))
	}
	if len(got.Levels) == 0 || got.Endpoint == "" {
		t.Errorf("spec is missing endpoint/levels: %+v", got)
	}
	// The published example must itself be a payload the server accepts —
	// a documented example that fails validation is worse than none.
	if len(got.Example) != 2 {
		t.Fatalf("example: want 2 entries, got %d", len(got.Example))
	}
	h := &Handler{}
	for i, entry := range got.Example {
		raw, _ := json.Marshal(entry)
		res := h.validateOne(i, raw)
		if len(res.Errors) > 0 {
			t.Errorf("example entry %d does not validate: %v", i, res.Errors)
		}
		if len(res.Unknown) > 0 {
			t.Errorf("example entry %d uses unknown fields: %+v", i, res.Unknown)
		}
	}
}
