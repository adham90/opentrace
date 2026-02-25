package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// listUsersTool returns the tool definition.
func listUsersTool() mcp.Tool {
	return mcp.NewTool("list_users",
		mcp.WithDescription("List all user accounts with their roles, status, and MCP access. Admin only."),
	)
}

// listUsersHandler returns a handler that lists all users.
func listUsersHandler(us store.UserStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		users, err := us.List(ctx)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to list users: %v", err)), nil
		}

		type userSummary struct {
			ID          string `json:"id"`
			Email       string `json:"email"`
			DisplayName string `json:"display_name"`
			Role        string `json:"role"`
			IsActive    bool   `json:"is_active"`
			MCPEnabled  bool   `json:"mcp_enabled"`
			CreatedAt   string `json:"created_at"`
		}

		summaries := make([]userSummary, len(users))
		for i, u := range users {
			summaries[i] = userSummary{
				ID:          u.ID,
				Email:       u.Email,
				DisplayName: u.DisplayName,
				Role:        string(u.Role),
				IsActive:    u.IsActive,
				MCPEnabled:  u.MCPEnabled,
				CreatedAt:   u.CreatedAt.Format("2006-01-02 15:04:05"),
			}
		}

		data, err := json.Marshal(summaries)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal users: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// updateUserRoleTool returns the tool definition.
func updateUserRoleTool() mcp.Tool {
	return mcp.NewTool("update_user_role",
		mcp.WithDescription("Change a user's role to 'admin' or 'member'. Admin only."),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User ID (from list_users)")),
		mcp.WithString("role", mcp.Required(), mcp.Description("New role: 'admin' or 'member'")),
	)
}

// updateUserRoleHandler returns a handler that updates a user's role.
func updateUserRoleHandler(us store.UserStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		userID, _ := args["user_id"].(string)
		if userID == "" {
			return mcp.NewToolResultError("user_id is required. Use list_users to find user IDs."), nil
		}

		roleStr, _ := args["role"].(string)
		if roleStr != "admin" && roleStr != "member" {
			return mcp.NewToolResultError("role must be 'admin' or 'member'."), nil
		}

		role := store.UserRole(roleStr)
		user, err := us.Update(ctx, userID, store.UpdateUserParams{Role: &role})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to update role: %v", err)), nil
		}

		result := map[string]any{
			"status":  "updated",
			"user_id": user.ID,
			"email":   user.Email,
			"role":    string(user.Role),
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// toggleUserActiveTool returns the tool definition.
func toggleUserActiveTool() mcp.Tool {
	return mcp.NewTool("toggle_user_active",
		mcp.WithDescription("Enable or disable a user account. Disabled users cannot log in or use MCP. Admin only."),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User ID (from list_users)")),
		mcp.WithBoolean("is_active", mcp.Required(), mcp.Description("true to enable, false to disable the account")),
	)
}

// toggleUserActiveHandler returns a handler that enables/disables a user.
func toggleUserActiveHandler(us store.UserStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		userID, _ := args["user_id"].(string)
		if userID == "" {
			return mcp.NewToolResultError("user_id is required. Use list_users to find user IDs."), nil
		}

		active, ok := args["is_active"].(bool)
		if !ok {
			return mcp.NewToolResultError("is_active is required (true or false)."), nil
		}

		user, err := us.Update(ctx, userID, store.UpdateUserParams{IsActive: &active})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to update user: %v", err)), nil
		}

		status := "enabled"
		if !user.IsActive {
			status = "disabled"
		}

		result := map[string]any{
			"status":    status,
			"user_id":   user.ID,
			"email":     user.Email,
			"is_active": user.IsActive,
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// deleteUserTool returns the tool definition.
func deleteUserTool() mcp.Tool {
	return mcp.NewTool("delete_user",
		mcp.WithDescription("Permanently delete a user account. This cannot be undone. Admin only."),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User ID to delete (from list_users)")),
	)
}

// deleteUserHandler returns a handler that deletes a user.
func deleteUserHandler(us store.UserStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		userID, _ := args["user_id"].(string)
		if userID == "" {
			return mcp.NewToolResultError("user_id is required. Use list_users to find user IDs."), nil
		}

		// Verify user exists
		user, err := us.GetByID(ctx, userID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("user not found: %v", err)), nil
		}

		if err := us.Delete(ctx, userID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to delete user: %v", err)), nil
		}

		result := map[string]any{
			"status":  "deleted",
			"user_id": userID,
			"email":   user.Email,
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
