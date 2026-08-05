package tfconvert

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/zclconf/go-cty/cty"
)

// ctyToAny converts a literal cty.Value (always string/bool/number/null
// for anything this package's own deliberately narrow expression
// vocabulary produces -- a collection-typed LiteralValueExpr never
// occurs in practice, HCL always represents list/object literals as
// TupleConsExpr/ObjectConsExpr instead) into the same untyped Go shape
// encoding/json.Unmarshal would have produced, matching blueprint/
// decode.go's own decodedField.Value convention exactly.
func ctyToAny(v cty.Value) (any, error) {
	if v.IsNull() {
		return nil, nil
	}
	if !v.IsWhollyKnown() {
		return nil, fmt.Errorf("value isn't statically known")
	}
	switch v.Type() {
	case cty.String:
		return v.AsString(), nil
	case cty.Bool:
		return v.True(), nil
	case cty.Number:
		f, _ := v.AsBigFloat().Float64()
		return f, nil
	default:
		return nil, fmt.Errorf("literal value of type %s isn't supported", v.Type().FriendlyName())
	}
}

func numberText(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func boolText(b bool) string {
	return strconv.FormatBool(b)
}

// marshalCompact is a small json.Marshal wrapper -- used to render a
// {"$ref":...} marker's own canonical text for template embedding
// (convertTemplate) exactly matching the shape blueprint/decode.go's own
// embeddedRefPattern regex expects to find.
func marshalCompact(v any) ([]byte, error) {
	return json.Marshal(v)
}
