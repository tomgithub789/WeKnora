package structuredoutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

var completeFenceRE = regexp.MustCompile("(?s)```(?:json)?[ \\t]*\\r?\\n?(.*?)```")

var errAmbiguousJSON = errors.New("multiple distinct JSON values found in model response")

type decodedJSON struct {
	value    any
	source   string
	strategy string
	repairs  []string
}

func decodeJSON(raw string, acceptance port.Acceptance) (decodedJSON, error) {
	direct := strings.TrimSpace(raw)
	if direct == "" {
		return decodedJSON{}, io.EOF
	}
	if value, err := strictDecode(direct); err == nil {
		return decodedJSON{value: value, source: direct, strategy: "direct"}, nil
	} else if acceptance == port.AcceptanceStrict {
		return decodedJSON{}, err
	}

	normalized, transportRepairs := normalizeTransport(raw)
	if value, err := strictDecode(normalized); err == nil {
		return decodedJSON{
			value: value, source: normalized, strategy: "direct", repairs: transportRepairs,
		}, nil
	}

	fences := completeFenceCandidates(normalized)
	if candidate, ok, err := selectStrictCandidate(fences, "fence", transportRepairs); ok || err != nil {
		return candidate, err
	}

	balanced := balancedJSONCandidates(normalized)
	if candidate, ok, err := selectStrictCandidate(balanced, "balanced_extract", transportRepairs); ok || err != nil {
		return candidate, err
	}

	lexicalCandidates := fences
	if len(lexicalCandidates) == 0 {
		lexicalCandidates = balanced
	}
	if len(lexicalCandidates) == 0 {
		lexicalCandidates = []string{normalized}
	}

	type repairStep struct {
		name string
		fn   func(string) string
	}
	steps := []repairStep{
		{name: "literal_controls", fn: escapeLiteralControls},
		{name: "invalid_backslash", fn: repairInvalidBackslashes},
		{name: "trailing_commas", fn: removeTrailingCommas},
	}

	current := append([]string(nil), lexicalCandidates...)
	repairs := append([]string(nil), transportRepairs...)
	for _, step := range steps {
		changed := false
		for i := range current {
			next := step.fn(current[i])
			if next != current[i] {
				changed = true
			}
			current[i] = next
		}
		if !changed {
			continue
		}
		repairs = append(repairs, step.name)
		if candidate, ok, err := selectStrictCandidate(current, "safe_lexical_repair", repairs); ok || err != nil {
			return candidate, err
		}
	}

	_, err := strictDecode(normalized)
	return decodedJSON{}, err
}

func strictDecode(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("multiple top-level JSON values")
		}
		return nil, fmt.Errorf("decode trailing content: %w", err)
	}
	return value, nil
}

func normalizeTransport(raw string) (string, []string) {
	repairs := make([]string, 0, 3)
	value := raw
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
		repairs = append(repairs, "invalid_utf8")
	}
	if strings.ContainsRune(value, '\x00') {
		value = strings.ReplaceAll(value, "\x00", "")
		repairs = append(repairs, "nul")
	}
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "\ufeff") {
		value = strings.TrimPrefix(value, "\ufeff")
		value = strings.TrimSpace(value)
		repairs = append(repairs, "bom")
	}
	return value, repairs
}

func completeFenceCandidates(raw string) []string {
	matches := completeFenceRE.FindAllStringSubmatch(raw, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		candidate := strings.TrimSpace(match[1])
		if candidate != "" {
			out = append(out, candidate)
		}
	}
	return out
}

func selectStrictCandidate(candidates []string, strategy string, repairs []string) (decodedJSON, bool, error) {
	type selected struct {
		value  any
		source string
	}
	valid := make(map[string]selected)
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		value, err := strictDecode(raw)
		if err != nil {
			continue
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			continue
		}
		valid[string(canonical)] = selected{value: value, source: raw}
	}
	if len(valid) == 0 {
		return decodedJSON{}, false, nil
	}
	if len(valid) > 1 {
		return decodedJSON{}, false, errAmbiguousJSON
	}
	for _, item := range valid {
		return decodedJSON{
			value: item.value, source: item.source, strategy: strategy, repairs: append([]string(nil), repairs...),
		}, true, nil
	}
	return decodedJSON{}, false, nil
}

// balancedJSONCandidates extracts only complete top-level objects or arrays.
// If an outer value is truncated, nested balanced fragments are deliberately
// not returned.
func balancedJSONCandidates(raw string) []string {
	var out []string
	for start := 0; start < len(raw); {
		for start < len(raw) && raw[start] != '{' && raw[start] != '[' {
			start++
		}
		if start >= len(raw) {
			break
		}

		stack := []byte{raw[start]}
		inString := false
		escaped := false
		closedAt := -1
		invalid := false
		for i := start + 1; i < len(raw); i++ {
			ch := raw[i]
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if ch == '\\' {
					escaped = true
					continue
				}
				if ch == '"' {
					inString = false
				}
				continue
			}
			switch ch {
			case '"':
				inString = true
			case '{', '[':
				stack = append(stack, ch)
			case '}', ']':
				if len(stack) == 0 || !matchingBrackets(stack[len(stack)-1], ch) {
					invalid = true
					closedAt = i
					break
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					closedAt = i
				}
			}
			if invalid || closedAt >= 0 {
				break
			}
		}
		if closedAt >= start && !invalid && len(stack) == 0 {
			out = append(out, strings.TrimSpace(raw[start:closedAt+1]))
			start = closedAt + 1
			continue
		}
		// An unmatched outer container makes all nested fragments unsafe.
		break
	}
	return out
}

func matchingBrackets(open, close byte) bool {
	return (open == '{' && close == '}') || (open == '[' && close == ']')
}

func escapeLiteralControls(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			out.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			out.WriteByte(ch)
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			out.WriteByte(ch)
			continue
		}
		if inString && ch < 0x20 {
			switch ch {
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			default:
				fmt.Fprintf(&out, `\u%04x`, ch)
			}
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func repairInvalidBackslashes(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '"' {
			inString = !inString
			out.WriteByte(ch)
			continue
		}
		if !inString || ch != '\\' {
			out.WriteByte(ch)
			continue
		}
		if i+1 >= len(raw) {
			out.WriteString(`\\`)
			continue
		}
		next := raw[i+1]
		if strings.ContainsRune(`"\/bfnrt`, rune(next)) {
			out.WriteByte(ch)
			out.WriteByte(next)
			i++
			continue
		}
		if next == 'u' && i+5 < len(raw) && allHex(raw[i+2:i+6]) {
			out.WriteString(raw[i : i+6])
			i += 5
			continue
		}
		out.WriteString(`\\`)
	}
	return out.String()
}

func allHex(raw string) bool {
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')) {
			return false
		}
	}
	return true
}

func removeTrailingCommas(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			out.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out.WriteByte(ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\r' || raw[j] == '\n') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}
