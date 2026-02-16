package store

import (
	"context"
	"testing"
)

func TestSettingsStore_DefaultRetention(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	settings, err := ss.GetRetention(ctx)
	if err != nil {
		t.Fatalf("GetRetention: %v", err)
	}
	if settings.RetentionDays != 30 {
		t.Errorf("default retention = %d, want 30", settings.RetentionDays)
	}
}

func TestSettingsStore_SetAndGet(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	err := ss.SetRetention(ctx, RetentionSettings{RetentionDays: 7})
	if err != nil {
		t.Fatalf("SetRetention: %v", err)
	}

	settings, err := ss.GetRetention(ctx)
	if err != nil {
		t.Fatalf("GetRetention: %v", err)
	}
	if settings.RetentionDays != 7 {
		t.Errorf("retention = %d, want 7", settings.RetentionDays)
	}
}

func TestSettingsStore_KeepForever(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	err := ss.SetRetention(ctx, RetentionSettings{RetentionDays: 0})
	if err != nil {
		t.Fatalf("SetRetention: %v", err)
	}

	settings, err := ss.GetRetention(ctx)
	if err != nil {
		t.Fatalf("GetRetention: %v", err)
	}
	if settings.RetentionDays != 0 {
		t.Errorf("retention = %d, want 0", settings.RetentionDays)
	}
}

func TestSettingsStore_Upsert(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	// First write
	err := ss.SetRetention(ctx, RetentionSettings{RetentionDays: 14})
	if err != nil {
		t.Fatalf("SetRetention first: %v", err)
	}

	// Second write overwrites
	err = ss.SetRetention(ctx, RetentionSettings{RetentionDays: 60})
	if err != nil {
		t.Fatalf("SetRetention second: %v", err)
	}

	settings, err := ss.GetRetention(ctx)
	if err != nil {
		t.Fatalf("GetRetention: %v", err)
	}
	if settings.RetentionDays != 60 {
		t.Errorf("retention = %d, want 60", settings.RetentionDays)
	}
}

func TestSettingsStore_CORSOrigins(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	// Default: empty string
	origins, err := ss.GetCORSOrigins(ctx)
	if err != nil {
		t.Fatalf("GetCORSOrigins (empty): %v", err)
	}
	if origins != "" {
		t.Errorf("expected empty default, got %q", origins)
	}

	// Set origins
	err = ss.SetCORSOrigins(ctx, "https://example.com,https://app.example.com")
	if err != nil {
		t.Fatalf("SetCORSOrigins: %v", err)
	}

	origins, err = ss.GetCORSOrigins(ctx)
	if err != nil {
		t.Fatalf("GetCORSOrigins (after set): %v", err)
	}
	if origins != "https://example.com,https://app.example.com" {
		t.Errorf("expected origins, got %q", origins)
	}

	// Overwrite
	err = ss.SetCORSOrigins(ctx, "*")
	if err != nil {
		t.Fatalf("SetCORSOrigins (overwrite): %v", err)
	}

	origins, err = ss.GetCORSOrigins(ctx)
	if err != nil {
		t.Fatalf("GetCORSOrigins (after overwrite): %v", err)
	}
	if origins != "*" {
		t.Errorf("expected *, got %q", origins)
	}

	// Clear
	err = ss.SetCORSOrigins(ctx, "")
	if err != nil {
		t.Fatalf("SetCORSOrigins (clear): %v", err)
	}

	origins, err = ss.GetCORSOrigins(ctx)
	if err != nil {
		t.Fatalf("GetCORSOrigins (after clear): %v", err)
	}
	if origins != "" {
		t.Errorf("expected empty after clear, got %q", origins)
	}
}

func TestSettingsStore_APIKey(t *testing.T) {
	db := setupTestDB(t)
	ss := NewSettingsStore(db)
	ctx := context.Background()

	// Default: empty string
	key, err := ss.GetAPIKey(ctx)
	if err != nil {
		t.Fatalf("GetAPIKey (empty): %v", err)
	}
	if key != "" {
		t.Errorf("expected empty default, got %q", key)
	}

	// Set a key
	err = ss.SetAPIKey(ctx, "ot_abc123")
	if err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	key, err = ss.GetAPIKey(ctx)
	if err != nil {
		t.Fatalf("GetAPIKey (after set): %v", err)
	}
	if key != "ot_abc123" {
		t.Errorf("expected ot_abc123, got %q", key)
	}

	// Overwrite
	err = ss.SetAPIKey(ctx, "ot_new456")
	if err != nil {
		t.Fatalf("SetAPIKey (overwrite): %v", err)
	}

	key, err = ss.GetAPIKey(ctx)
	if err != nil {
		t.Fatalf("GetAPIKey (after overwrite): %v", err)
	}
	if key != "ot_new456" {
		t.Errorf("expected ot_new456, got %q", key)
	}
}
