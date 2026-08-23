package hsql

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// arrayTimeFormat is the layout used for temporal array elements in the text
// form (RFC 3339 with nanoseconds, round-trippable via time.Parse).
const arrayTimeFormat = time.RFC3339Nano

// arrayNullToken marks a NULL element in the array text form.
const arrayNullToken = "NULL"

// Array is a structured SQL ARRAY parameter. Values are encoded using the
// server-provided parameter metadata for the array's element type.
type Array struct {
	Values []any
}

// NewArray returns a structured ARRAY parameter for prepared statements.
func NewArray(values ...any) Array {
	return Array{Values: values}
}

// encodeArrayText renders a decoded ARRAY as its lossless text form:
//
//	[1,2,NULL]  ["a","b,c","he said \"hi\""]  [ab12ff]  ["2024-05-01T10:00:00Z"]
//
// String (and string-like: DECIMAL, INTERVAL) elements are double-quoted with
// Go escaping, temporal elements are quoted RFC 3339, binary elements are bare
// hex, numeric/boolean elements are bare literals and NULL elements are the
// bare token NULL. ScanArray parses this form back without ambiguity.
func encodeArrayText(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = encodeArrayElement(v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func encodeArrayElement(v any) string {
	switch x := v.(type) {
	case nil:
		return arrayNullToken
	case string:
		return strconv.Quote(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case []byte:
		return hex.EncodeToString(x)
	case time.Time:
		return strconv.Quote(x.Format(arrayTimeFormat))
	case proto.ArrayValue:
		return encodeArrayText(x.Values)
	default:
		return strconv.Quote(fmt.Sprint(x))
	}
}

// ScanArray returns an sql.Scanner that parses a SQL ARRAY result into the Go
// slice pointed to by dest (e.g. *[]int64, *[]string, *[]float64, *[]bool,
// *[]time.Time, *[][]byte or *[]any). It mirrors the pq.Array pattern:
//
//	var ids []int64
//	err := db.QueryRow("SELECT ids FROM t").Scan(hsql.ScanArray(&ids))
//
// ARRAY values are delivered by the driver in a lossless text form (see
// encodeArrayText): strings are quoted and escaped, so elements containing
// commas, quotes or the literal NULL round-trip exactly.
func ScanArray(dest any) sql.Scanner {
	return &arrayScanner{dest: dest}
}

type arrayScanner struct{ dest any }

func (a *arrayScanner) Scan(src any) error {
	rv := reflect.ValueOf(a.dest)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("hsql: ScanArray destination must be a pointer to a slice, got %T", a.dest)
	}
	slice := rv.Elem()
	if src == nil {
		slice.Set(reflect.Zero(slice.Type()))
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("hsql: cannot scan %T into an array", src)
	}

	elems, err := parseArrayElements(s)
	if err != nil {
		return err
	}
	out := reflect.MakeSlice(slice.Type(), len(elems), len(elems))
	for i, e := range elems {
		if e.null {
			// NULL element -> zero value of the element type.
			continue
		}
		if err := setArrayElement(out.Index(i), e); err != nil {
			return fmt.Errorf("hsql: array element %d: %w", i, err)
		}
	}
	slice.Set(out)
	return nil
}

// arrayElem is one parsed element of the array text form.
type arrayElem struct {
	raw    string // unquoted content
	quoted bool   // true when the element was a quoted string in the text
	null   bool   // true for the bare NULL token
}

// parseArrayElements splits the driver's array text into elements, honoring
// double-quoted strings with backslash escapes. An empty array "[]" yields an
// empty slice.
func parseArrayElements(s string) ([]arrayElem, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("hsql: malformed array text %q", s)
	}
	s = s[1 : len(s)-1]
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	var out []arrayElem
	for i := 0; i < len(s); {
		// Skip leading whitespace within the element.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i < len(s) && s[i] == '"' {
			// Quoted string element: find the closing quote, honoring escapes.
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' {
					j += 2
					continue
				}
				if s[j] == '"' {
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("hsql: unterminated quoted array element in %q", s)
			}
			unquoted, err := strconv.Unquote(s[i : j+1])
			if err != nil {
				return nil, fmt.Errorf("hsql: malformed quoted array element %q: %w", s[i:j+1], err)
			}
			out = append(out, arrayElem{raw: unquoted, quoted: true})
			i = j + 1
			// Skip to and past the separating comma.
			for i < len(s) && s[i] != ',' {
				i++
			}
			i++
			continue
		}
		// Bare element: runs to the next top-level comma.
		j := strings.IndexByte(s[i:], ',')
		var token string
		if j < 0 {
			token = s[i:]
			i = len(s)
		} else {
			token = s[i : i+j]
			i += j + 1
		}
		token = strings.TrimSpace(token)
		if token == arrayNullToken {
			out = append(out, arrayElem{null: true})
			continue
		}
		out = append(out, arrayElem{raw: token})
	}
	return out, nil
}

func setArrayElement(dst reflect.Value, e arrayElem) error {
	// Concrete element types with no useful reflect kind.
	switch dst.Type() {
	case reflect.TypeOf(time.Time{}):
		t, err := time.Parse(arrayTimeFormat, e.raw)
		if err != nil {
			return err
		}
		dst.Set(reflect.ValueOf(t))
		return nil
	case reflect.TypeOf([]byte(nil)):
		if e.quoted {
			dst.SetBytes([]byte(e.raw))
			return nil
		}
		b, err := hex.DecodeString(e.raw)
		if err != nil {
			return err
		}
		dst.SetBytes(b)
		return nil
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(e.raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(e.raw, 10, 64)
		if err != nil {
			return err
		}
		dst.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(e.raw, 10, 64)
		if err != nil {
			return err
		}
		dst.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(e.raw, 64)
		if err != nil {
			return err
		}
		dst.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(e.raw)
		if err != nil {
			return err
		}
		dst.SetBool(b)
	case reflect.Interface:
		dst.Set(reflect.ValueOf(inferArrayElement(e)))
	default:
		return fmt.Errorf("unsupported element type %s", dst.Type())
	}
	return nil
}

// inferArrayElement maps a parsed element to a Go value for []any
// destinations: quoted elements stay strings, bare tokens become int64,
// float64 or bool when they parse as one, and anything else stays a string.
func inferArrayElement(e arrayElem) any {
	if e.quoted {
		return e.raw
	}
	if n, err := strconv.ParseInt(e.raw, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(e.raw, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(e.raw); err == nil {
		return b
	}
	return e.raw
}
