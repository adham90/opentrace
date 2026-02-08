package agent

import "fmt"

// ValidateArgs checks that args satisfy the given tool parameter schema.
// Required params must be present. Type checking validates that values match
// the declared type. Extra args are ignored.
func ValidateArgs(params []ToolParam, args map[string]any) error {
	for _, p := range params {
		v, ok := args[p.Name]
		if !ok {
			if p.Required {
				return fmt.Errorf("missing required parameter %q", p.Name)
			}
			continue
		}
		if err := checkType(p.Name, p.Type, v); err != nil {
			return err
		}
	}
	return nil
}

func checkType(name, typ string, v any) error {
	switch typ {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("parameter %q: expected string, got %T", name, v)
		}
	case "int":
		// JSON numbers decode as float64; accept both float64 and int.
		switch v.(type) {
		case float64, int:
		default:
			return fmt.Errorf("parameter %q: expected int, got %T", name, v)
		}
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("parameter %q: expected bool, got %T", name, v)
		}
	}
	return nil
}
