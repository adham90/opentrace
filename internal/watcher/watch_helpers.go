package watcher

import "github.com/adham90/opentrace/pkg/store"

// baselineMetricValue extracts the baseline value for a watch's metric
// from its stored baseline snapshot. Returns 0 if no baseline exists.
func baselineMetricValue(w *store.Watch) float64 {
	if w.BaselineJSON == nil {
		return 0
	}
	switch w.Metric {
	case store.WatchMetricErrorRate:
		return w.BaselineJSON.ErrorRate
	case store.WatchMetricResponseTime:
		return w.BaselineJSON.AvgResponseMs
	case store.WatchMetricP95Response:
		return w.BaselineJSON.P95ResponseMs
	case store.WatchMetricLogCount:
		return float64(w.BaselineJSON.LogCount)
	case store.WatchMetricErrorCount:
		return float64(w.BaselineJSON.ErrorCount)
	case store.WatchMetricSQLCount:
		return w.BaselineJSON.SQLCount
	case store.WatchMetricCacheHitRate:
		return w.BaselineJSON.CacheHitRate
	default:
		return 0
	}
}
