package hsql

// Array is a structured SQL ARRAY parameter. Values are encoded using the
// server-provided parameter metadata for the array's element type.
type Array struct {
	Values []any
}

// NewArray returns a structured ARRAY parameter for prepared statements.
func NewArray(values ...any) Array {
	return Array{Values: values}
}
