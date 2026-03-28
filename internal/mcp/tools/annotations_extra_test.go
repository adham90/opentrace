package tools

import (
	"testing"
)

func TestControllerFromPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"app/controllers/users_controller.rb", "UsersController"},
		{"app/controllers/user_sessions_controller.rb", "UserSessionsController"},
		{"internal/handlers/payment.go", "payment"},
		{"/api/health.js", "health"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := controllerFromPath(tt.input)
			if got != tt.want {
				t.Errorf("controllerFromPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"users", "Users"},
		{"user_sessions", "UserSessions"},
		{"users_controller", "UsersController"},
		{"", ""},
		{"hello_world_test", "HelloWorldTest"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toPascalCase(tt.input)
			if got != tt.want {
				t.Errorf("toPascalCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
