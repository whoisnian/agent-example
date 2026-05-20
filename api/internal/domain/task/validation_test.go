package task

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateTitle(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		want    string
	}{
		{"empty", "", true, ""},
		{"whitespace only", "   ", true, ""},
		{"trims", "  hello  ", false, "hello"},
		{"oversize", strings.Repeat("a", 201), true, ""},
		{"max len", strings.Repeat("a", 200), false, strings.Repeat("a", 200)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateTitle(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if tc.wantErr {
				var iiErr *ErrInvalidInput
				if !errors.As(err, &iiErr) {
					t.Fatalf("err not ErrInvalidInput: %T", err)
				}
				if iiErr.Field != "title" {
					t.Fatalf("wrong field %q", iiErr.Field)
				}
			}
		})
	}
}

func TestValidateTaskType(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"valid kebab", "code-gen", false},
		{"single letter", "a", false},
		{"empty", "", true},
		{"leading digit", "1code", true},
		{"underscore", "code_gen", true},
		{"uppercase", "Code-Gen", true},
		{"too long", strings.Repeat("a", 65), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateTaskType(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestValidatePrompt(t *testing.T) {
	if _, err := validatePrompt(""); err == nil {
		t.Fatalf("empty prompt should fail")
	}
	if _, err := validatePrompt("hello"); err != nil {
		t.Fatalf("short prompt should pass: %v", err)
	}
	if _, err := validatePrompt(strings.Repeat("a", 16385)); err == nil {
		t.Fatalf("oversize prompt should fail")
	}
}

func TestResolveLane(t *testing.T) {
	d := "default"
	if got, err := resolveLane(nil, d); err != nil || got != d {
		t.Fatalf("nil should fall back: got=%q err=%v", got, err)
	}
	v := ""
	if _, err := resolveLane(&v, d); err == nil {
		t.Fatalf("empty string should error")
	}
	v = "Default"
	if _, err := resolveLane(&v, d); err == nil {
		t.Fatalf("uppercase should error")
	}
	v = "canary"
	if got, _ := resolveLane(&v, d); got != "canary" {
		t.Fatalf("wrong lane: %q", got)
	}
	v = strings.Repeat("a", 33)
	if _, err := resolveLane(&v, d); err == nil {
		t.Fatalf("oversize should error")
	}
}

func TestValidateParams(t *testing.T) {
	// nil and "null" both canonicalise to "{}".
	if got, err := validateParams(nil); err != nil || string(got) != "{}" {
		t.Fatalf("nil: got=%q err=%v", got, err)
	}
	if got, err := validateParams([]byte("null")); err != nil || string(got) != "{}" {
		t.Fatalf("null: got=%q err=%v", got, err)
	}
	// Arrays / scalars rejected.
	if _, err := validateParams([]byte("[]")); err == nil {
		t.Fatalf("array should error")
	}
	if _, err := validateParams([]byte(`"oops"`)); err == nil {
		t.Fatalf("scalar should error")
	}
	// Valid object passes through and is canonicalised.
	if got, err := validateParams([]byte(` { "k" : "v" } `)); err != nil || string(got) != `{"k":"v"}` {
		t.Fatalf("object: got=%q err=%v", got, err)
	}
	// Oversize.
	big := make(map[string]string)
	for i := 0; i < 5000; i++ {
		big[fmtKey(i)] = "v"
	}
	raw, _ := json.Marshal(big)
	if _, err := validateParams(raw); err == nil {
		t.Fatalf("oversize should error")
	}
	// Invalid JSON.
	if _, err := validateParams([]byte("{")); err == nil {
		t.Fatalf("invalid JSON should error")
	}
}

func fmtKey(i int) string {
	// avoid fmt import; tiny base-10 stringer is enough for the test seed.
	if i == 0 {
		return "k0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return "k" + string(digits)
}

func TestIsActive(t *testing.T) {
	for _, s := range []string{"pending", "queued", "running", "paused", "cancelling"} {
		if !IsActive(s) {
			t.Fatalf("%s should be active", s)
		}
	}
	for _, s := range []string{"cancelled", "succeeded", "failed", "unknown"} {
		if IsActive(s) {
			t.Fatalf("%s should NOT be active", s)
		}
	}
}
