package hsql

import (
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

// Error is a database error returned by the HSQLDB server. It carries the
// server message, the SQLState, and the vendor-specific error code.
type Error struct {
	Message   string
	SQLState  string
	ErrorCode int32
}

// Error implements the error interface.
func (e *Error) Error() string {
	return fmt.Sprintf("hsql: %s (SQLState %s, code %d)", e.Message, e.SQLState, e.ErrorCode)
}

// errorFromResult converts an ERROR-mode Result into an *Error.
func errorFromResult(r *proto.Result) *Error {
	return &Error{Message: r.Message, SQLState: r.SQLState, ErrorCode: r.ErrorCode}
}
