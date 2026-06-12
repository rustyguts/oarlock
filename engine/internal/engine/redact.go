package engine

import (
	"encoding/json"
	"sort"
	"strings"
)

// redactor strips secret values out of anything the engine persists or logs
// (hard rule 6: secrets never in definitions or logs). Values are replaced
// longest-first so overlapping secrets can't leave fragments behind.
type redactor struct {
	values []string
}

const redactedMark = "[redacted]"

func newRedactor(secrets map[string]string) *redactor {
	r := &redactor{}
	for _, v := range secrets {
		// Tiny values would shred unrelated text; real secrets are longer.
		if len(v) >= 4 {
			r.values = append(r.values, v)
			// JSON-escaped form too, so secrets inside marshaled payloads match.
			if esc, err := json.Marshal(v); err == nil {
				escaped := string(esc[1 : len(esc)-1])
				if escaped != v {
					r.values = append(r.values, escaped)
				}
			}
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

func (r *redactor) String(s string) string {
	if r == nil {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, redactedMark)
	}
	return s
}

func (r *redactor) JSON(b []byte) []byte {
	if r == nil || len(b) == 0 {
		return b
	}
	return []byte(r.String(string(b)))
}
