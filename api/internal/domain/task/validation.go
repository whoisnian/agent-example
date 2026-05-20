package task

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Validation limits — design D12.
const (
	maxTitleLen      = 200
	maxPromptLen     = 16384
	maxTaskTypeLen   = 64
	maxLaneLen       = 32
	maxParamsBytes   = 32 * 1024
)

var (
	reTaskType = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	reLane     = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// validateTitle enforces "required, 1..200 chars, trimmed". The trim is
// applied before length checks so all-whitespace fails the empty rule.
func validateTitle(raw string) (string, error) {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "", newInvalidInput("title", "must not be empty")
	}
	if len(t) > maxTitleLen {
		return "", newInvalidInput("title", "exceeds 200 characters")
	}
	return t, nil
}

// validateTaskType enforces the kebab-case slug. Trim is rejected because
// task_type is an identifier, not free text.
func validateTaskType(raw string) (string, error) {
	if raw == "" {
		return "", newInvalidInput("task_type", "must not be empty")
	}
	if len(raw) > maxTaskTypeLen {
		return "", newInvalidInput("task_type", "exceeds 64 characters")
	}
	if !reTaskType.MatchString(raw) {
		return "", newInvalidInput("task_type", "must match ^[a-z][a-z0-9-]{0,63}$")
	}
	return raw, nil
}

// validatePrompt enforces "required, 1..16384 chars" without trim — leading
// whitespace can be intentional in code-gen prompts.
func validatePrompt(raw string) (string, error) {
	if raw == "" {
		return "", newInvalidInput("prompt", "must not be empty")
	}
	if len(raw) > maxPromptLen {
		return "", newInvalidInput("prompt", "exceeds 16384 characters")
	}
	return raw, nil
}

// resolveLane implements design D6: absent / null → fallback to configured
// default; an explicit empty / pattern-violating value is rejected as
// invalid_input.
func resolveLane(raw *string, fallback string) (string, error) {
	if raw == nil {
		return fallback, nil
	}
	v := *raw
	if v == "" || !reLane.MatchString(v) {
		return "", newInvalidInput("lane", "must match ^[a-z0-9-]{1,32}$")
	}
	return v, nil
}

// validateParams returns canonical JSON bytes for the params field. The
// caller passes either a non-nil RawMessage from the request body, or nil to
// indicate "absent / null" (canonicalised to `{}`).
func validateParams(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	// Decode-then-re-encode to canonicalise whitespace and assert structure.
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, newInvalidInput("params", "must be a valid JSON object")
	}
	// MVP shape constraint: params must be an object (not array / scalar).
	if _, ok := v.(map[string]any); !ok {
		// `null` is treated as absent.
		if v == nil {
			return []byte("{}"), nil
		}
		return nil, newInvalidInput("params", "must be a JSON object")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, newInvalidInput("params", "could not be re-encoded")
	}
	if len(out) > maxParamsBytes {
		return nil, newInvalidInput("params", "exceeds 32 KiB serialised")
	}
	return out, nil
}
