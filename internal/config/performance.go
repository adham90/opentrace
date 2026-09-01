package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	ResourceProfileBalanced = "balanced"
	ResourceProfileTiny     = "tiny"
)

// PerformanceConfig is one coherent memory/concurrency budget. Keeping these
// values together prevents independent caches from each assuming they own most
// of a small VM.
type PerformanceConfig struct {
	Profile              string
	GOMAXPROCS           int
	MemoryLimitMB        int
	ColumnCacheMB        int
	WALCacheMB           int
	IndexCacheMB         int
	SQLiteCacheMB        int
	SQLiteMmapMB         int
	SearchConcurrency    int
	MaxConcurrentQueries int
	SealChunkEntries     int
	IngestRatePerMinute  int
	IngestInFlightMB     int
	PostprocessWorkers   int
	PostprocessQueue     int
	AccessLog            bool
	WALCompressBody      bool
}

func balancedPerformanceConfig(devMode bool) PerformanceConfig {
	return PerformanceConfig{
		Profile:              ResourceProfileBalanced,
		ColumnCacheMB:        64,
		WALCacheMB:           64,
		IndexCacheMB:         16,
		SQLiteCacheMB:        64,
		SQLiteMmapMB:         30,
		SearchConcurrency:    8,
		MaxConcurrentQueries: 8,
		SealChunkEntries:     50000,
		IngestRatePerMinute:  600000,
		IngestInFlightMB:     64,
		PostprocessWorkers:   2,
		PostprocessQueue:     128,
		AccessLog:            devMode,
		WALCompressBody:      true,
	}
}

// EffectivePerformance supplies balanced defaults for Config literals created
// by tests and embedders that predate the nested performance settings.
func (c *Config) EffectivePerformance() PerformanceConfig {
	if c == nil {
		return balancedPerformanceConfig(false)
	}
	if c.Performance.Profile == "" {
		return balancedPerformanceConfig(c.DevMode)
	}
	return c.Performance
}

func tinyPerformanceConfig(devMode bool) PerformanceConfig {
	return PerformanceConfig{
		Profile:              ResourceProfileTiny,
		GOMAXPROCS:           1,
		MemoryLimitMB:        160,
		ColumnCacheMB:        8,
		WALCacheMB:           16,
		IndexCacheMB:         8,
		SQLiteCacheMB:        8,
		SQLiteMmapMB:         4,
		SearchConcurrency:    1,
		MaxConcurrentQueries: 2,
		SealChunkEntries:     8192,
		IngestRatePerMinute:  600000,
		IngestInFlightMB:     16,
		PostprocessWorkers:   1,
		PostprocessQueue:     32,
		AccessLog:            devMode,
		// The WAL is transient and its sealed body column is compressed later.
		// Skipping the first compression trades disk bandwidth for much less CPU.
		WALCompressBody: false,
	}
}

func loadPerformanceConfig(devMode bool) (PerformanceConfig, error) {
	profile := strings.ToLower(envOrDefault("OPENTRACE_RESOURCE_PROFILE", ResourceProfileBalanced))
	var cfg PerformanceConfig
	switch profile {
	case ResourceProfileBalanced:
		cfg = balancedPerformanceConfig(devMode)
	case ResourceProfileTiny:
		cfg = tinyPerformanceConfig(devMode)
	default:
		return PerformanceConfig{}, fmt.Errorf("invalid value for OPENTRACE_RESOURCE_PROFILE: %q (want balanced or tiny)", profile)
	}

	intOverrides := []struct {
		key string
		dst *int
		min int
		max int
	}{
		{"OPENTRACE_GOMAXPROCS", &cfg.GOMAXPROCS, 0, 1024},
		{"OPENTRACE_MEMORY_LIMIT_MB", &cfg.MemoryLimitMB, 0, 1 << 20},
		{"OPENTRACE_COLUMN_CACHE_MB", &cfg.ColumnCacheMB, 0, 1 << 20},
		{"OPENTRACE_WAL_CACHE_MB", &cfg.WALCacheMB, 0, 1 << 20},
		{"OPENTRACE_INDEX_CACHE_MB", &cfg.IndexCacheMB, 0, 1 << 20},
		{"OPENTRACE_SQLITE_CACHE_MB", &cfg.SQLiteCacheMB, 0, 1 << 20},
		{"OPENTRACE_SQLITE_MMAP_MB", &cfg.SQLiteMmapMB, 0, 1 << 20},
		{"OPENTRACE_SEARCH_CONCURRENCY", &cfg.SearchConcurrency, 1, 1024},
		{"OPENTRACE_MAX_CONCURRENT_QUERIES", &cfg.MaxConcurrentQueries, 1, 1024},
		{"OPENTRACE_SEAL_CHUNK_ENTRIES", &cfg.SealChunkEntries, 512, 50000},
		{"OPENTRACE_INGEST_RATE_LIMIT_PER_MINUTE", &cfg.IngestRatePerMinute, 1, 1 << 30},
		{"OPENTRACE_INGEST_INFLIGHT_MB", &cfg.IngestInFlightMB, 10, 1 << 20},
		{"OPENTRACE_POSTPROCESS_WORKERS", &cfg.PostprocessWorkers, 1, 1024},
		{"OPENTRACE_POSTPROCESS_QUEUE", &cfg.PostprocessQueue, 1, 1 << 20},
	}
	for _, override := range intOverrides {
		if err := envIntOverride(override.key, override.dst, override.min, override.max); err != nil {
			return PerformanceConfig{}, err
		}
	}
	if err := envBoolOverride("OPENTRACE_ACCESS_LOG", &cfg.AccessLog); err != nil {
		return PerformanceConfig{}, err
	}
	if err := envBoolOverride("OPENTRACE_WAL_COMPRESS_BODY", &cfg.WALCompressBody); err != nil {
		return PerformanceConfig{}, err
	}
	return cfg, nil
}

func envIntOverride(key string, dst *int, minValue, maxValue int) error {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return fmt.Errorf("invalid value for %s: %q (want an integer from %d to %d)", key, raw, minValue, maxValue)
	}
	*dst = value
	return nil
}

func envBoolOverride(key string, dst *bool) error {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("invalid value for %s: %q (want true or false)", key, raw)
	}
	*dst = value
	return nil
}
