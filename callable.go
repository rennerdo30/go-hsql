package hsql

import (
	"database/sql"
	"fmt"
	"reflect"
	"time"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// Stored-procedure OUT / INOUT parameters.
//
// Bind an output parameter of a CALL statement with the standard sql.Out
// wrapper:
//
//	var total int64
//	_, err := db.ExecContext(ctx, "CALL add_totals(?, ?)", 5, sql.Out{Dest: &total})
//
// The server answers a procedure CALL with a CALL_RESPONSE result carrying the
// final values of every parameter; the driver copies the values of registered
// OUT parameters into their destinations. For INOUT parameters set In: true so
// the current value of Dest is sent as the input.

// checkOutValue validates an sql.Out binding at CheckNamedValue time.
func checkOutValue(out sql.Out) error {
	if out.Dest == nil {
		return fmt.Errorf("hsql: sql.Out destination must not be nil")
	}
	rv := reflect.ValueOf(out.Dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("hsql: sql.Out destination must be a non-nil pointer, got %T", out.Dest)
	}
	return nil
}

// splitOutParams replaces sql.Out values in a positional parameter slice with
// their input values (nil for pure OUT, the dereferenced destination for
// INOUT) and returns the OUT destinations indexed by parameter position.
func splitOutParams(values []any) ([]any, map[int]sql.Out, error) {
	var outs map[int]sql.Out
	for i, v := range values {
		out, ok := v.(sql.Out)
		if !ok {
			continue
		}
		if err := checkOutValue(out); err != nil {
			return nil, nil, err
		}
		if outs == nil {
			outs = make(map[int]sql.Out)
		}
		outs[i] = out
		if out.In {
			in := reflect.ValueOf(out.Dest).Elem().Interface()
			if err := checkOutInputValue(in); err != nil {
				return nil, nil, fmt.Errorf("hsql: INOUT parameter %d: %w", i+1, err)
			}
			values[i] = in
		} else {
			values[i] = nil
		}
	}
	return values, outs, nil
}

// checkOutInputValue rejects INOUT input values the wire encoder would
// otherwise silently coerce to a zero value.
func checkOutInputValue(v any) error {
	switch v.(type) {
	case nil, bool, string, []byte, time.Time,
		int, int8, int16, int32, int64, float32, float64:
		return nil
	default:
		return fmt.Errorf("unsupported input value type %T", v)
	}
}

// applyOutParams copies the parameter values of a CALL_RESPONSE into the
// registered sql.Out destinations. Binding sql.Out on a statement that does
// not answer with a CALL_RESPONSE (only procedure CALLs do) is an error rather
// than a silent no-op.
func (c *conn) applyOutParams(res *proto.Result, outs map[int]sql.Out) error {
	if len(outs) == 0 || res == nil {
		return nil
	}
	if res.Mode != proto.ModeCallResponse {
		return fmt.Errorf("hsql: statement returned no OUT parameter values (response mode %d); sql.Out requires a stored-procedure CALL", res.Mode)
	}
	if res.RowSet == nil || len(res.RowSet.Rows) == 0 {
		return fmt.Errorf("hsql: CALL response carried no parameter values")
	}
	row := res.RowSet.Rows[0]
	for idx, out := range outs {
		if idx >= len(row) {
			return fmt.Errorf("hsql: CALL response has %d parameters, OUT parameter %d out of range", len(row), idx+1)
		}
		v, err := c.resolveValue(row[idx])
		if err != nil {
			return err
		}
		if err := assignOutValue(out.Dest, v); err != nil {
			return fmt.Errorf("hsql: OUT parameter %d: %w", idx+1, err)
		}
	}
	return nil
}

// assignOutValue stores a driver value into an OUT destination pointer,
// covering sql.Scanner destinations (including the sql.Null* types), the
// standard driver value types and common Go scalar kinds.
func assignOutValue(dest any, v any) error {
	if scanner, ok := dest.(sql.Scanner); ok {
		return scanner.Scan(v)
	}
	switch d := dest.(type) {
	case *any:
		*d = v
		return nil
	case *time.Time:
		if t, ok := v.(time.Time); ok {
			*d = t
			return nil
		}
		return fmt.Errorf("cannot assign %T to *time.Time", v)
	case *[]byte:
		switch x := v.(type) {
		case nil:
			*d = nil
		case []byte:
			*d = x
		case string:
			*d = []byte(x)
		default:
			return fmt.Errorf("cannot assign %T to *[]byte", v)
		}
		return nil
	}

	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dest)
	}
	elem := rv.Elem()
	if v == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	switch elem.Kind() {
	case reflect.String:
		switch x := v.(type) {
		case string:
			elem.SetString(x)
		case []byte:
			elem.SetString(string(x))
		default:
			elem.SetString(fmt.Sprint(x))
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := asOutInt64(v)
		if err != nil {
			return err
		}
		if elem.OverflowInt(n) {
			return fmt.Errorf("value %d overflows %s", n, elem.Type())
		}
		elem.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := asOutInt64(v)
		if err != nil {
			return err
		}
		if n < 0 || elem.OverflowUint(uint64(n)) {
			return fmt.Errorf("value %d overflows %s", n, elem.Type())
		}
		elem.SetUint(uint64(n))
	case reflect.Float32, reflect.Float64:
		switch x := v.(type) {
		case float64:
			elem.SetFloat(x)
		case int64:
			elem.SetFloat(float64(x))
		default:
			return fmt.Errorf("cannot assign %T to %s", v, elem.Type())
		}
	case reflect.Bool:
		if b, ok := v.(bool); ok {
			elem.SetBool(b)
		} else {
			return fmt.Errorf("cannot assign %T to %s", v, elem.Type())
		}
	default:
		return fmt.Errorf("unsupported OUT destination type %T", dest)
	}
	return nil
}

func asOutInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case float64:
		return int64(x), nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot convert %T to integer", v)
	}
}
