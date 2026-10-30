package activity

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const (
	maxBody     = 64 << 10 // a request or answer bigger than this is not read for the log
	maxString   = 300      // longer strings are cut
	redactedTag = "[redacted]"
)

// A key with one of these in it never reaches the log, whatever its value. Short words ("otp", "pin") are matched as whole words of a
// snake_case key (otp_code, new_pin), so "shipping" is not caught.
var (
	secretParts  = map[string]bool{"otp": true, "pin": true, "refresh": true, "authorization": true, "passcode": true, "credential": true, "credentials": true}
	secretInside = []string{"token", "password", "secret"}
)

func isSecret(key string) bool {
	k := strings.ToLower(key)
	for _, s := range secretInside {
		if strings.Contains(k, s) {
			return true
		}
	}
	for _, part := range strings.FieldsFunc(k, func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		if secretParts[part] {
			return true
		}
	}
	return false
}

// redact returns the JSON body with every secret value replaced and long strings cut, or nil when the body is not a JSON object or
// array. Numbers keep their exact text (decimals travel as strings, and as numbers they must not be rounded by a float).
func redact(body []byte) json.RawMessage {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || (body[0] != '{' && body[0] != '[') {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	out, err := json.Marshal(clean(v))
	if err != nil {
		return nil
	}
	return out
}

func clean(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if isSecret(k) {
				x[k] = redactedTag
				continue
			}
			x[k] = clean(val)
		}
		return x
	case []any:
		for i, val := range x {
			x[i] = clean(val)
		}
		return x
	case string:
		return cut(x)
	default:
		return v
	}
}

func cut(s string) string {
	if len(s) <= maxString {
		return s
	}
	end := maxString
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
