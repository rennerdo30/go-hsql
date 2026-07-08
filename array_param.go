package hsql

import (
	"fmt"

	"github.com/rennerdo30/go-hsql/internal/proto"
)

func (c *conn) prepareArrayParams(req *proto.Result) error {
	if req.Mode != proto.ModeExecute || req.ParamMeta == nil {
		return nil
	}
	n := int(req.ParamMeta.ColumnCount)
	for i := 0; i < n; i++ {
		if i >= len(req.ParamValues) || req.ParamValues[i] == nil {
			continue
		}
		if req.ParamMeta.Types[i].Code != proto.SQLArray {
			continue
		}
		switch v := req.ParamValues[i].(type) {
		case Array:
			req.ParamValues[i] = proto.ArrayValue{Values: v.Values}
		case *Array:
			if v == nil {
				return fmt.Errorf("hsql: nil ARRAY parameter")
			}
			req.ParamValues[i] = proto.ArrayValue{Values: v.Values}
		case proto.ArrayValue:
		default:
			return fmt.Errorf("hsql: cannot bind %T as ARRAY", req.ParamValues[i])
		}
	}
	return nil
}
