package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

func HandleListUsers(ctx context.Context, d AdminDeps) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	users, err := d.UserStore.List(ctx)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to list users: %v", err)), nil
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

	return JSONResult(summaries)
}

func HandleUpdateRole(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID := ArgString(args, "user_id")
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	roleStr := ArgString(args, "role")
	if roleStr != "admin" && roleStr != "member" {
		return NewToolResultError("role must be 'admin' or 'member'."), nil
	}

	role := store.UserRole(roleStr)
	user, err := d.UserStore.Update(ctx, userID, store.UpdateUserParams{Role: &role})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update role: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status":  "updated",
		"user_id": user.ID,
		"email":   user.Email,
		"role":    string(user.Role),
	})
}

func HandleToggleActive(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID := ArgString(args, "user_id")
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	active, ok := args["is_active"].(bool)
	if !ok {
		return NewToolResultError("is_active is required (true or false)."), nil
	}

	user, err := d.UserStore.Update(ctx, userID, store.UpdateUserParams{IsActive: &active})
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to update user: %v", err)), nil
	}

	status := "enabled"
	if !user.IsActive {
		status = "disabled"
	}

	return JSONResult(map[string]any{
		"status":    status,
		"user_id":   user.ID,
		"email":     user.Email,
		"is_active": user.IsActive,
	})
}

func HandleDeleteUser(ctx context.Context, d AdminDeps, args map[string]any) (*CallToolResult, error) {
	if d.UserStore == nil {
		return NewToolResultError("UserStore not configured"), nil
	}

	userID := ArgString(args, "user_id")
	if userID == "" {
		return NewToolResultError("user_id is required. Use admin with action=users to find user IDs."), nil
	}

	user, err := d.UserStore.GetByID(ctx, userID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("user not found: %v", err)), nil
	}

	if err := d.UserStore.Delete(ctx, userID); err != nil {
		return NewToolResultError(fmt.Sprintf("failed to delete user: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status":  "deleted",
		"user_id": userID,
		"email":   user.Email,
	})
}
