package proto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
)

// Canonical returns the canonical JSON encoding of v (docs/api-spec.md §1.3):
// UTF-8, object keys sorted by Unicode code point, no insignificant
// whitespace, integers in shortest decimal form, standard string escaping.
//
// v may be any value encoding/json can marshal, or a json.RawMessage /
// []byte holding JSON. v1 payloads contain only strings, booleans, null,
// arrays, objects and integers; non-integer numbers are rejected so that the
// encoding stays deterministic.
func Canonical(v any) ([]byte, error) {
	var raw []byte
	switch t := v.(type) {
	case json.RawMessage:
		raw = t
	case []byte:
		raw = t
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("canonical: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("canonical: trailing data after JSON value")
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, generic); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func writeCanonical(out *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(t))
	case string:
		return writeString(out, t)
	case json.Number:
		i, err := strconv.ParseInt(t.String(), 10, 64)
		if err != nil {
			return fmt.Errorf("canonical: only integer numbers are allowed, got %q", t.String())
		}
		out.WriteString(strconv.FormatInt(i, 10))
	case []any:
		out.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeCanonical(out, e); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// Byte order of UTF-8 equals Unicode code point order.
		sort.Strings(keys)
		out.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeString(out, k); err != nil {
				return err
			}
			out.WriteByte(':')
			if err := writeCanonical(out, t[k]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("canonical: unsupported type %T", v)
	}
	return nil
}

func writeString(out *bytes.Buffer, s string) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	out.Write(bytes.TrimRight(b.Bytes(), "\n"))
	return nil
}

// ContentHash returns "sha256:" + hex(sha256(canonical_json(payload))).
func ContentHash(payload any) (string, error) {
	c, err := Canonical(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
