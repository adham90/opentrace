package digest

import "testing"

func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name     string
		alerts   AlertSummary
		watchers WatcherSummary
		want     DigestStatus
	}{
		{
			name: "healthy when no alerts and no errors",
			want: StatusHealthy,
		},
		{
			name:   "info_only when only info alerts",
			alerts: AlertSummary{Info: 3},
			want:   StatusInfoOnly,
		},
		{
			name:   "needs_attention when warnings present",
			alerts: AlertSummary{Warning: 2, Info: 1},
			want:   StatusNeedsAttention,
		},
		{
			name:   "critical when critical alerts present",
			alerts: AlertSummary{Critical: 1, Warning: 2, Info: 1},
			want:   StatusCritical,
		},
		{
			name:     "degraded when watchers in error (overrides critical)",
			alerts:   AlertSummary{Critical: 1},
			watchers: WatcherSummary{InError: 1},
			want:     StatusDegraded,
		},
		{
			name:     "degraded when watchers in error but no alerts",
			watchers: WatcherSummary{InError: 2},
			want:     StatusDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStatus(tt.alerts, tt.watchers)
			if got != tt.want {
				t.Errorf("DeriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
