package digest

import "testing"

func TestTrends_AlertsChangePercent(t *testing.T) {
	tests := []struct {
		name string
		t    Trends
		want float64
	}{
		{
			name: "zero previous returns 0",
			t:    Trends{AlertsPrevCount: 0, AlertsCurrentCount: 5},
			want: 0,
		},
		{
			name: "same count returns 0",
			t:    Trends{AlertsPrevCount: 10, AlertsCurrentCount: 10},
			want: 0,
		},
		{
			name: "increase",
			t:    Trends{AlertsPrevCount: 10, AlertsCurrentCount: 15},
			want: 50,
		},
		{
			name: "decrease",
			t:    Trends{AlertsPrevCount: 10, AlertsCurrentCount: 7},
			want: -30,
		},
		{
			name: "double",
			t:    Trends{AlertsPrevCount: 5, AlertsCurrentCount: 15},
			want: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.t.AlertsChangePercent()
			if got != tt.want {
				t.Errorf("AlertsChangePercent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTrends_FailedRunsChangePercent(t *testing.T) {
	tests := []struct {
		name string
		t    Trends
		want float64
	}{
		{
			name: "zero previous returns 0",
			t:    Trends{FailedRunsPrev: 0, FailedRunsCurrent: 3},
			want: 0,
		},
		{
			name: "increase from 2 to 4",
			t:    Trends{FailedRunsPrev: 2, FailedRunsCurrent: 4},
			want: 100,
		},
		{
			name: "decrease from 4 to 2",
			t:    Trends{FailedRunsPrev: 4, FailedRunsCurrent: 2},
			want: -50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.t.FailedRunsChangePercent()
			if got != tt.want {
				t.Errorf("FailedRunsChangePercent() = %v, want %v", got, tt.want)
			}
		})
	}
}
