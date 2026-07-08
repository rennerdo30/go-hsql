package hsql

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Array is a structured SQL ARRAY parameter. Values are encoded using the
// server-provided parameter metadata for the array's element type.
type Array struct {
	Values []any
}

// NewArray returns a structured ARRAY parameter for prepared statements.
func NewArray(values ...any) Array {
	return Array{Values: values}
}

// ScanArray returns an sql.Scanner that parses a SQL ARRAY result into the Go
// slice pointed to by dest (e.g. *[]int64, *[]string, *[]float64, *[]bool). It
// mirrors the pq.Array pattern:
//
//	var ids []int64
//	err := db.QueryRow("SELECT ids FROM t").Scan(hsql.ScanArray(&ids))
//
// ARRAY values are delivered by the driver in the textual form "[a,b,c]" with
// NULL elements written as NULL; ScanArray converts each element to dest's
// element type. Note this text form is ambiguous for string elements that
// themselves contain commas, "]" or the literal NULL — use a typed numeric or
// boolean slice for lossless results.
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

	elems := parseArrayElements(s)
	out := reflect.MakeSlice(slice.Type(), len(elems), len(elems))
	for i, e := range elems {
		if e == nil {
			// NULL element -> zero value of the element type.
			continue
		}
		if err := setArrayElement(out.Index(i), *e); err != nil {
			return fmt.Errorf("hsql: array element %d: %w", i, err)
		}
	}
	slice.Set(out)
	return nil
}

// parseArrayElements splits the driver's "[a,b,c]" array text into element
// strings, with nil for NULL elements. Empty array "[]" yields an empty slice.
func parseArrayElements(s string) []*string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]*string, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "NULL" {
			out[i] = nil
			continue
		}
		v := p
		out[i] = &v
	}
	return out
}

func setArrayElement(dst reflect.Value, s string) error {
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(s)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		dst.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return err
		}
		dst.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		dst.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		dst.SetBool(b)
	default:
		return fmt.Errorf("unsupported element type %s", dst.Type())
	}
	return nil
}
