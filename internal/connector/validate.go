package connector

import (
	"fmt"
	"strconv"
)

// ValidateArgs checks that args satisfy the given tool parameter schema.
// Required params must be present. Type checking validates that values match
// the declared type. LLMs often send numbers as strings ("10" instead of 10),
// so this function coerces compatible types and updates args in-place.
func ValidateArgs(params []ToolParam, args map[string]any) error {
	for _, p := range params {
		v, ok := args[p.Name]
		if !ok {
			if p.Required {
				return fmt.Errorf("missing required parameter %q", p.Name)
			}
			continue
		}
		coerced, err := coerceType(p.Name, p.Type, v)
		if err != nil {
			return err
		}
		args[p.Name] = coerced
	}
	return nil
}

func coerceType(name, typ string, v any) (any, error) {
	switch typ {
	case "string":
		switch val := v.(type) {
		case string:
			return val, nil
		case float64:
			if val == float64(int64(val)) {
				return strconv.FormatInt(int64(val), 10), nil
			}
			return strconv.FormatFloat(val, 'f', -1, 64), nil
		default:
			return nil, fmt.Errorf("parameter %q: expected string, got %T", name, v)
		}
	case "int":
		switch val := v.(type) {
		case float64:
			return val, nil
		case int:
			return float64(val), nil
		case string:
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return nil, fmt.Errorf("parameter %q: expected int, got string %q", name, val)
			}
			return f, nil
		default:
			return nil, fmt.Errorf("parameter %q: expected int, got %T", name, v)
		}
	case "bool":
		switch val := v.(type) {
		case bool:
			return val, nil
		case string:
			b, err := strconv.ParseBool(val)
			if err != nil {
				return nil, fmt.Errorf("parameter %q: expected bool, got string %q", name, val)
			}
			return b, nil
		default:
			return nil, fmt.Errorf("parameter %q: expected bool, got %T", name, v)
		}
	}
	return v, nil
}
