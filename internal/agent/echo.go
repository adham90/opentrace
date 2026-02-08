package agent

import (
	"context"
	"fmt"
)

// EchoTool returns a built-in tool that echoes its input message.
// Used for testing the agent loop without real connectors.
func EchoTool() Tool {
	return Tool{
		Name:        "echo",
		Description: "Echoes back the provided message. Useful for testing.",
		Params: []ToolParam{
			{Name: "message", Type: "string", Required: true},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			msg, ok := args["message"].(string)
			if !ok {
				return "", fmt.Errorf("echo: message must be a string")
			}
			return msg, nil
		},
	}
}
