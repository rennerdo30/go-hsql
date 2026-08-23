package hsql

import (
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

func (c *conn) prepareArrayParams(req *proto.Result) error {
	types, values := requestParamSlots(req)
	for i := range types {
		if i >= len(values) || values[i] == nil {
			continue
		}
		if types[i].Code != proto.SQLArray {
			continue
		}
		switch v := values[i].(type) {
		case Array:
			values[i] = proto.ArrayValue{Values: v.Values}
		case *Array:
			if v == nil {
				return fmt.Errorf("hsql: nil ARRAY parameter")
			}
			values[i] = proto.ArrayValue{Values: v.Values}
		case proto.ArrayValue:
		default:
			return fmt.Errorf("hsql: cannot bind %T as ARRAY", values[i])
		}
	}
	return nil
}

// requestParamSlots returns the parameter type descriptors and the mutable
// value slice of an outgoing request that binds values: EXECUTE binds one row
// against the statement's parameter metadata, UPDATE_RESULT binds one extended
// row against the cursor's update metadata. Other modes bind nothing.
func requestParamSlots(req *proto.Result) ([]proto.ColumnType, []any) {
	switch req.Mode {
	case proto.ModeExecute:
		if req.ParamMeta == nil {
			return nil, nil
		}
		return req.ParamMeta.Types[:req.ParamMeta.ColumnCount], req.ParamValues
	case proto.ModeUpdateResult:
		if req.Meta == nil || len(req.BatchRows) != 1 {
			return nil, nil
		}
		return req.Meta.Types[:req.Meta.ColumnCount], req.BatchRows[0]
	default:
		return nil, nil
	}
}
