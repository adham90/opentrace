package store

import (
	"context"
	"testing"
	"time"
)

func TestJourneyStore_BuildSessions(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-1", UserID: "user-42",
			Message: "req1",
			RequestSummary: &RequestSummary{
				Controller: "Home", Action: "index", Method: "GET",
				Path: "/", Status: 200, DurationMs: 100,
			},
		},
		{
			Timestamp: now.Add(-25 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-1", UserID: "user-42",
			Message: "req2",
			RequestSummary: &RequestSummary{
				Controller: "Users", Action: "show", Method: "GET",
				Path: "/users/42", Status: 200, DurationMs: 80,
			},
		},
		{
			Timestamp: now.Add(-20 * time.Minute), Level: "ERROR", Service: "api",
			SessionID: "sess-1", UserID: "user-42",
			Message: "req3",
			RequestSummary: &RequestSummary{
				Controller: "Orders", Action: "create", Method: "POST",
				Path: "/orders", Status: 500, DurationMs: 300,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	if err := js.BuildSessions(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatalf("BuildSessions: %v", err)
	}

	session, err := js.GetSession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if session.UserID != "user-42" {
		t.Errorf("user_id = %q, want user-42", session.UserID)
	}
	if session.RequestCount != 3 {
		t.Errorf("request_count = %d, want 3", session.RequestCount)
	}
	if session.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", session.ErrorCount)
	}
	if !session.HasError {
		t.Error("expected has_error = true")
	}
	if session.EntryPath != "Home#index" {
		t.Errorf("entry_path = %q, want Home#index", session.EntryPath)
	}
	if session.ExitPath != "Orders#create" {
		t.Errorf("exit_path = %q, want Orders#create", session.ExitPath)
	}
	if session.ExitStatus != 500 {
		t.Errorf("exit_status = %d, want 500", session.ExitStatus)
	}
}

func TestJourneyStore_ListSessions(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-a", UserID: "user-1",
			Message: "ok",
			RequestSummary: &RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 50,
			},
		},
		{
			Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-b", UserID: "user-2",
			Message: "ok",
			RequestSummary: &RequestSummary{
				Controller: "A", Action: "c", Status: 200, DurationMs: 60,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	js.BuildSessions(ctx, now.Add(-time.Hour))

	sessions, err := js.ListSessions(ctx, SessionListParams{
		Since: now.Add(-time.Hour),
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(sessions))
	}

	// Filter by user
	sessions, err = js.ListSessions(ctx, SessionListParams{
		UserID: "user-1",
		Since:  now.Add(-time.Hour),
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("ListSessions by user: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions for user-1, want 1", len(sessions))
	}
}

func TestJourneyStore_GetSessionRequests(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-steps", UserID: "user-1",
			Message: "step1",
			RequestSummary: &RequestSummary{
				Controller: "Dashboard", Action: "show", Method: "GET",
				Status: 200, DurationMs: 120, SQLCount: 5,
			},
		},
		{
			Timestamp: now.Add(-25 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-steps", UserID: "user-1",
			Message: "step2",
			RequestSummary: &RequestSummary{
				Controller: "Projects", Action: "index", Method: "GET",
				Status: 200, DurationMs: 80, SQLCount: 3,
			},
		},
		{
			Timestamp: now.Add(-20 * time.Minute), Level: "ERROR", Service: "api",
			SessionID: "sess-steps", UserID: "user-1",
			Message:        "step3",
			ExceptionClass: "ActiveRecord::RecordNotFound",
			RequestSummary: &RequestSummary{
				Controller: "Projects", Action: "show", Method: "GET",
				Status: 404, DurationMs: 15,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	steps, err := js.GetSessionRequests(ctx, "sess-steps")
	if err != nil {
		t.Fatalf("GetSessionRequests: %v", err)
	}
	if len(steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(steps))
	}
	if steps[0].Controller != "Dashboard" || steps[0].Action != "show" {
		t.Errorf("first step = %s#%s, want Dashboard#show", steps[0].Controller, steps[0].Action)
	}
	if steps[2].Status != 404 {
		t.Errorf("third step status = %d, want 404", steps[2].Status)
	}
}

func TestJourneyStore_GetUserJourney(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-x", UserID: "user-99",
			Message: "r1",
			RequestSummary: &RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 50,
			},
		},
		{
			Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-y", UserID: "user-99",
			Message: "r2",
			RequestSummary: &RequestSummary{
				Controller: "C", Action: "d", Status: 200, DurationMs: 70,
			},
		},
		{
			Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-z", UserID: "user-100",
			Message: "r3",
			RequestSummary: &RequestSummary{
				Controller: "E", Action: "f", Status: 200, DurationMs: 30,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	steps, err := js.GetUserJourney(ctx, "user-99", now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("GetUserJourney: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("got %d steps for user-99, want 2", len(steps))
	}
}

func TestJourneyStore_CommonPaths(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	// Create 3 sessions following the same path: A#b → C#d
	for i, sid := range []string{"s1", "s2", "s3"} {
		entries := []LogEntry{
			{
				Timestamp: now.Add(-time.Duration(30+i*10) * time.Minute), Level: "INFO", Service: "api",
				SessionID: sid, UserID: "u",
				Message: "step1",
				RequestSummary: &RequestSummary{
					Controller: "Home", Action: "index", Status: 200, DurationMs: 100,
				},
			},
			{
				Timestamp: now.Add(-time.Duration(29+i*10) * time.Minute), Level: "INFO", Service: "api",
				SessionID: sid, UserID: "u",
				Message: "step2",
				RequestSummary: &RequestSummary{
					Controller: "Products", Action: "list", Status: 200, DurationMs: 80,
				},
			},
		}
		ls.BatchInsert(ctx, entries)
	}

	paths, err := js.CommonPaths(ctx, PathAnalysisParams{
		Since:          now.Add(-2 * time.Hour),
		MinOccurrences: 2,
		PathLength:     3,
	})
	if err != nil {
		t.Fatalf("CommonPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("expected at least one common path")
	}
	if paths[0].Count < 3 {
		t.Errorf("top path count = %d, want >= 3", paths[0].Count)
	}
	if len(paths[0].Steps) != 2 {
		t.Errorf("top path steps = %d, want 2", len(paths[0].Steps))
	}
}

func TestJourneyStore_FunnelCRUD(t *testing.T) {
	db := setupTestDB(t)
	js := NewJourneyStore(db)
	ctx := context.Background()

	funnel, err := js.CreateFunnel(ctx, Funnel{
		Name:    "Signup Flow",
		Service: "api",
		Steps: []FunnelStep{
			{Controller: "Sessions", Action: "new", Label: "Visit signup"},
			{Controller: "Sessions", Action: "create", Label: "Submit form"},
			{Controller: "Onboarding", Action: "show", Label: "Complete onboarding"},
		},
	})
	if err != nil {
		t.Fatalf("CreateFunnel: %v", err)
	}
	if funnel.ID == 0 {
		t.Error("expected non-zero funnel ID")
	}

	got, err := js.GetFunnel(ctx, funnel.ID)
	if err != nil {
		t.Fatalf("GetFunnel: %v", err)
	}
	if got.Name != "Signup Flow" {
		t.Errorf("name = %q, want Signup Flow", got.Name)
	}
	if len(got.Steps) != 3 {
		t.Errorf("steps = %d, want 3", len(got.Steps))
	}

	all, err := js.ListFunnels(ctx)
	if err != nil {
		t.Fatalf("ListFunnels: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("len = %d, want 1", len(all))
	}

	if err := js.DeleteFunnel(ctx, funnel.ID); err != nil {
		t.Fatalf("DeleteFunnel: %v", err)
	}

	_, err = js.GetFunnel(ctx, funnel.ID)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestJourneyStore_AnalyzeFunnel(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Create funnel
	funnel, _ := js.CreateFunnel(ctx, Funnel{
		Name: "Checkout",
		Steps: []FunnelStep{
			{Controller: "Cart", Action: "show", Label: "View cart"},
			{Controller: "Checkout", Action: "new", Label: "Start checkout"},
			{Controller: "Orders", Action: "create", Label: "Place order"},
		},
	})

	// Session 1: completes all 3 steps
	entries := []LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s1", Message: "r1",
			RequestSummary: &RequestSummary{Controller: "Cart", Action: "show", Status: 200, DurationMs: 50}},
		{Timestamp: now.Add(-29 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s1", Message: "r2",
			RequestSummary: &RequestSummary{Controller: "Checkout", Action: "new", Status: 200, DurationMs: 60}},
		{Timestamp: now.Add(-28 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s1", Message: "r3",
			RequestSummary: &RequestSummary{Controller: "Orders", Action: "create", Status: 201, DurationMs: 200}},
	}
	ls.BatchInsert(ctx, entries)

	// Session 2: only first 2 steps
	entries2 := []LogEntry{
		{Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s2", Message: "r1",
			RequestSummary: &RequestSummary{Controller: "Cart", Action: "show", Status: 200, DurationMs: 50}},
		{Timestamp: now.Add(-19 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s2", Message: "r2",
			RequestSummary: &RequestSummary{Controller: "Checkout", Action: "new", Status: 200, DurationMs: 60}},
	}
	ls.BatchInsert(ctx, entries2)

	// Session 3: only step 1
	entries3 := []LogEntry{
		{Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s3", Message: "r1",
			RequestSummary: &RequestSummary{Controller: "Cart", Action: "show", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries3)

	result, err := js.AnalyzeFunnel(ctx, funnel.ID, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("AnalyzeFunnel: %v", err)
	}

	if result.TotalEntered != 3 {
		t.Errorf("total_entered = %d, want 3", result.TotalEntered)
	}
	if len(result.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(result.Steps))
	}
	if result.Steps[0].Count != 3 {
		t.Errorf("step 1 count = %d, want 3", result.Steps[0].Count)
	}
	if result.Steps[1].Count != 2 {
		t.Errorf("step 2 count = %d, want 2", result.Steps[1].Count)
	}
	if result.Steps[2].Count != 1 {
		t.Errorf("step 3 count = %d, want 1", result.Steps[2].Count)
	}
	if result.Steps[1].DropOff != 1 {
		t.Errorf("step 2 drop_off = %d, want 1", result.Steps[1].DropOff)
	}
}

func TestJourneyStore_RequestTimeline(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	timelineJSON := `[
		{"type":"sql","name":"User Load","start_ms":0,"duration_ms":5},
		{"type":"cache","name":"permissions","start_ms":5,"duration_ms":2,"details":{"hit":true}},
		{"type":"sql","name":"Report Load","start_ms":10,"duration_ms":1800}
	]`

	entries := []LogEntry{
		{
			Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-tl", RequestID: "req-123",
			Message: "request",
			RequestSummary: &RequestSummary{
				Controller: "Reports", Action: "show", Method: "GET",
				Path: "/reports/1", Status: 200, DurationMs: 2500,
				Timeline: timelineJSON,
			},
		},
	}
	n, err := ls.BatchInsert(ctx, entries)
	if err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	if n != 1 {
		t.Fatalf("inserted %d, want 1", n)
	}

	// Get the log ID
	logs, _ := ls.Search(ctx, LogSearchParams{Limit: 1})
	if len(logs) == 0 {
		t.Fatal("no logs found")
	}
	logID := logs[0].ID

	rt, err := js.GetRequestTimeline(ctx, logID)
	if err != nil {
		t.Fatalf("GetRequestTimeline: %v", err)
	}

	if rt.Controller != "Reports" || rt.Action != "show" {
		t.Errorf("controller#action = %s#%s, want Reports#show", rt.Controller, rt.Action)
	}
	if rt.DurationMs != 2500 {
		t.Errorf("duration = %v, want 2500", rt.DurationMs)
	}
	if len(rt.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(rt.Events))
	}
	if rt.Events[0].Type != "sql" || rt.Events[0].Name != "User Load" {
		t.Errorf("event[0] = %s/%s, want sql/User Load", rt.Events[0].Type, rt.Events[0].Name)
	}
	if rt.Bottleneck == nil {
		t.Fatal("expected bottleneck")
	}
	if rt.Bottleneck.Name != "Report Load" {
		t.Errorf("bottleneck = %s, want Report Load", rt.Bottleneck.Name)
	}
	if rt.Bottleneck.DurationMs != 1800 {
		t.Errorf("bottleneck duration = %v, want 1800", rt.Bottleneck.DurationMs)
	}
}

func TestJourneyStore_SessionTimeline(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	js := NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-wf", RequestID: "req-a",
			Message: "r1",
			RequestSummary: &RequestSummary{
				Controller: "Home", Action: "index", Status: 200, DurationMs: 100,
				Timeline: `[{"type":"sql","name":"Load","start_ms":0,"duration_ms":50}]`,
			},
		},
		{
			Timestamp: now.Add(-25 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-wf", RequestID: "req-b",
			Message: "r2",
			RequestSummary: &RequestSummary{
				Controller: "Users", Action: "show", Status: 200, DurationMs: 200,
				Timeline: `[{"type":"sql","name":"User","start_ms":0,"duration_ms":10},{"type":"view","name":"show.html","start_ms":10,"duration_ms":150}]`,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	timelines, err := js.GetSessionTimeline(ctx, "sess-wf")
	if err != nil {
		t.Fatalf("GetSessionTimeline: %v", err)
	}
	if len(timelines) != 2 {
		t.Fatalf("got %d timelines, want 2", len(timelines))
	}
	if len(timelines[0].Events) != 1 {
		t.Errorf("first request events = %d, want 1", len(timelines[0].Events))
	}
	if len(timelines[1].Events) != 2 {
		t.Errorf("second request events = %d, want 2", len(timelines[1].Events))
	}
	if timelines[1].Bottleneck == nil {
		t.Fatal("expected bottleneck for second request")
	}
	if timelines[1].Bottleneck.Name != "show.html" {
		t.Errorf("bottleneck = %s, want show.html", timelines[1].Bottleneck.Name)
	}
}

func TestJourneyStore_PromoteUserIDFromMetadata(t *testing.T) {
	db := setupTestDB(t)
	ls := NewLogStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []LogEntry{
		{
			Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api",
			Message:  "from metadata",
			Metadata: map[string]any{"user_id": "42", "session_id": "meta-sess"},
			RequestSummary: &RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 50,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	logs, _ := ls.Search(ctx, LogSearchParams{Limit: 1})
	if len(logs) == 0 {
		t.Fatal("no logs found")
	}
	if logs[0].UserID != "42" {
		t.Errorf("user_id = %q, want 42", logs[0].UserID)
	}
	if logs[0].SessionID != "meta-sess" {
		t.Errorf("session_id = %q, want meta-sess", logs[0].SessionID)
	}
}
