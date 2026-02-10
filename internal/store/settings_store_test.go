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
