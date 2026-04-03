package watcher

// baselineMetricValue is no longer needed — callers now use
// baselineValueForMetric(baseline, metric) from watch_conditions.go directly,
// or store.ConditionsMetric() to extract the metric from the conditions tree.
