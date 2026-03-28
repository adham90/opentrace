package tools

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// ArgString
// ---------------------------------------------------------------------------

func TestArgString_Present(t *testing.T) {
	args := map[string]any{"name": "hello"}
	if got := ArgString(args, "name"); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestArgString_Missing(t *testing.T) {
	args := map[string]any{}
	if got := ArgString(args, "name"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestArgString_WrongType(t *testing.T) {
	args := map[string]any{"name": 42.0}
	if got := ArgString(args, "name"); got != "" {
		t.Errorf("got %q, want empty for wrong type", got)
	}
}

// ---------------------------------------------------------------------------
// ArgStringDefault
// ---------------------------------------------------------------------------

func TestArgStringDefault_Present(t *testing.T) {
	args := map[string]any{"mode": "fast"}
	if got := ArgStringDefault(args, "mode", "slow"); got != "fast" {
		t.Errorf("got %q, want %q", got, "fast")
	}
}

func TestArgStringDefault_Missing(t *testing.T) {
	args := map[string]any{}
	if got := ArgStringDefault(args, "mode", "slow"); got != "slow" {
		t.Errorf("got %q, want default %q", got, "slow")
	}
}

func TestArgStringDefault_EmptyString(t *testing.T) {
	args := map[string]any{"mode": ""}
	if got := ArgStringDefault(args, "mode", "slow"); got != "slow" {
		t.Errorf("got %q, want default %q for empty string", got, "slow")
	}
}

// ---------------------------------------------------------------------------
// ArgInt
// ---------------------------------------------------------------------------

func TestArgInt_Present(t *testing.T) {
	args := map[string]any{"limit": float64(25)}
	if got := ArgInt(args, "limit", 10, 100); got != 25 {
		t.Errorf("got %d, want 25", got)
	}
}

func TestArgInt_Missing(t *testing.T) {
	args := map[string]any{}
	if got := ArgInt(args, "limit", 10, 100); got != 10 {
		t.Errorf("got %d, want default 10", got)
	}
}

func TestArgInt_Zero(t *testing.T) {
	args := map[string]any{"limit": float64(0)}
	if got := ArgInt(args, "limit", 10, 100); got != 10 {
		t.Errorf("got %d, want default 10 for zero", got)
	}
}

func TestArgInt_Negative(t *testing.T) {
	args := map[string]any{"limit": float64(-5)}
	if got := ArgInt(args, "limit", 10, 100); got != 10 {
		t.Errorf("got %d, want default 10 for negative", got)
	}
}

func TestArgInt_ExceedsMax(t *testing.T) {
	args := map[string]any{"limit": float64(500)}
	if got := ArgInt(args, "limit", 10, 100); got != 100 {
		t.Errorf("got %d, want capped 100", got)
	}
}

func TestArgInt_WrongType(t *testing.T) {
	args := map[string]any{"limit": "fifty"}
	if got := ArgInt(args, "limit", 10, 100); got != 10 {
		t.Errorf("got %d, want default 10 for wrong type", got)
	}
}

// ---------------------------------------------------------------------------
// ArgFloat
// ---------------------------------------------------------------------------

func TestArgFloat_Present(t *testing.T) {
	args := map[string]any{"threshold": 0.75}
	if got := ArgFloat(args, "threshold", 0.5); got != 0.75 {
		t.Errorf("got %f, want 0.75", got)
	}
}

func TestArgFloat_Missing(t *testing.T) {
	args := map[string]any{}
	if got := ArgFloat(args, "threshold", 0.5); got != 0.5 {
		t.Errorf("got %f, want default 0.5", got)
	}
}

// ---------------------------------------------------------------------------
// ArgBool
// ---------------------------------------------------------------------------

func TestArgBool_True(t *testing.T) {
	args := map[string]any{"verbose": true}
	if !ArgBool(args, "verbose") {
		t.Error("expected true")
	}
}

func TestArgBool_False(t *testing.T) {
	args := map[string]any{"verbose": false}
	if ArgBool(args, "verbose") {
		t.Error("expected false")
	}
}

func TestArgBool_Missing(t *testing.T) {
	args := map[string]any{}
	if ArgBool(args, "verbose") {
		t.Error("expected false for missing key")
	}
}

// ---------------------------------------------------------------------------
// ArgStringSlice
// ---------------------------------------------------------------------------

func TestArgStringSlice_FromAnySlice(t *testing.T) {
	args := map[string]any{"files": []any{"a.go", "b.go"}}
	got := ArgStringSlice(args, "files")
	if len(got) != 2 || got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("got %v, want [a.go b.go]", got)
	}
}

func TestArgStringSlice_FromStringSlice(t *testing.T) {
	args := map[string]any{"files": []string{"x.go"}}
	got := ArgStringSlice(args, "files")
	if len(got) != 1 || got[0] != "x.go" {
		t.Errorf("got %v, want [x.go]", got)
	}
}

func TestArgStringSlice_Missing(t *testing.T) {
	args := map[string]any{}
	got := ArgStringSlice(args, "files")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestArgStringSlice_WrongType(t *testing.T) {
	args := map[string]any{"files": "single"}
	got := ArgStringSlice(args, "files")
	if got != nil {
		t.Errorf("got %v, want nil for wrong type", got)
	}
}

// ---------------------------------------------------------------------------
// ArgDuration
// ---------------------------------------------------------------------------

func TestArgDuration_GoDuration(t *testing.T) {
	args := map[string]any{"window": "2h30m"}
	got := ArgDuration(args, "window", time.Hour)
	if got != 2*time.Hour+30*time.Minute {
		t.Errorf("got %v, want 2h30m", got)
	}
}

func TestArgDuration_DaySyntax(t *testing.T) {
	args := map[string]any{"window": "7d"}
	got := ArgDuration(args, "window", time.Hour)
	if got != 7*24*time.Hour {
		t.Errorf("got %v, want 168h (7d)", got)
	}
}

func TestArgDuration_Missing(t *testing.T) {
	args := map[string]any{}
	got := ArgDuration(args, "window", time.Hour)
	if got != time.Hour {
		t.Errorf("got %v, want default 1h", got)
	}
}

func TestArgDuration_Invalid(t *testing.T) {
	args := map[string]any{"window": "banana"}
	got := ArgDuration(args, "window", time.Hour)
	if got != time.Hour {
		t.Errorf("got %v, want default 1h for invalid input", got)
	}
}

func TestArgDuration_EmptyString(t *testing.T) {
	args := map[string]any{"window": ""}
	got := ArgDuration(args, "window", 30*time.Minute)
	if got != 30*time.Minute {
		t.Errorf("got %v, want default 30m for empty string", got)
	}
}
