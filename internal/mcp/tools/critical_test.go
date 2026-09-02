package tools

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func TestIsCriticalPath(t *testing.T) {
	patterns := []string{"/checkout", "billing"}

	cases := []struct {
		name   string
		fields []string
		want   bool
	}{
		{"no patterns match", []string{"GET /avatars"}, false},
		{"route match", []string{"POST /checkout/confirm"}, true},
		{"case insensitive", []string{"BillingError: card declined"}, true},
		{"matches any field", []string{"", "worker", "billing-svc"}, true},
		{"empty fields", []string{"", ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCriticalPath(patterns, tc.fields...); got != tc.want {
				t.Errorf("isCriticalPath(%v) = %v, want %v", tc.fields, got, tc.want)
			}
		})
	}

	if isCriticalPath(nil, "/checkout") {
		t.Error("with no patterns configured nothing should be critical")
	}
}

// alertOnlyWatchStore replays a fixed alert list; every other method is
// unimplemented because catch-up never calls them.
type alertOnlyWatchStore struct {
	store.WatchStore
	alerts []store.WatchAlert
}

func (s *alertOnlyWatchStore) ListAlerts(_ context.Context, _, _ string, _ int) ([]store.WatchAlert, error) {
	return s.alerts, nil
}

// TestCollectCatchup_MoneyPathFirst is the point of the flag: a warning on the
// money path outranks a critical on a sideshow, so the first line of a morning
// brief is the one that costs money.
func TestCollectCatchup_MoneyPathFirst(t *testing.T) {
	now := time.Now().UTC()
	ws := &alertOnlyWatchStore{alerts: []store.WatchAlert{
		{ID: "noisy", Summary: "error_rate on /health", Status: "pending", CreatedAt: now},
		{ID: "money", Summary: "slow POST /checkout", Status: "acknowledged", CreatedAt: now.Add(-time.Minute)},
		{ID: "tagged", Summary: "queue depth", Status: "acknowledged", Urgency: store.WatchUrgencyCritical, CreatedAt: now.Add(-2 * time.Minute)},
	}}

	items, truncated := CollectCatchup(context.Background(),
		OverviewDeps{WatchStore: ws, CriticalPaths: []string{"/checkout"}},
		"", now.Add(-time.Hour))

	if truncated {
		t.Errorf("truncated = true for %d items", len(items))
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}

	// The two money-path items lead; "noisy" is severity-critical but ranks
	// below both.
	front := map[string]bool{items[0].ID: true, items[1].ID: true}
	if !front["money"] || !front["tagged"] {
		t.Errorf("leading items = %q, %q; want money and tagged", items[0].ID, items[1].ID)
	}
	if items[2].ID != "noisy" {
		t.Errorf("last item = %q, want noisy", items[2].ID)
	}
	if !items[0].Critical || !items[1].Critical || items[2].Critical {
		t.Errorf("critical flags = %v/%v/%v, want true/true/false",
			items[0].Critical, items[1].Critical, items[2].Critical)
	}
}
