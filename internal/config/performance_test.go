package config

import "testing"

func clearPerformanceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"OPENTRACE_RESOURCE_PROFILE",
		"OPENTRACE_GOMAXPROCS",
		"OPENTRACE_MEMORY_LIMIT_MB",
		"OPENTRACE_COLUMN_CACHE_MB",
		"OPENTRACE_WAL_CACHE_MB",
		"OPENTRACE_INDEX_CACHE_MB",
		"OPENTRACE_SQLITE_CACHE_MB",
		"OPENTRACE_SQLITE_MMAP_MB",
		"OPENTRACE_SEARCH_CONCURRENCY",
		"OPENTRACE_MAX_CONCURRENT_QUERIES",
		"OPENTRACE_SEAL_CHUNK_ENTRIES",
		"OPENTRACE_INGEST_RATE_LIMIT_PER_MINUTE",
		"OPENTRACE_INGEST_INFLIGHT_MB",
		"OPENTRACE_POSTPROCESS_WORKERS",
		"OPENTRACE_POSTPROCESS_QUEUE",
		"OPENTRACE_ACCESS_LOG",
		"OPENTRACE_WAL_COMPRESS_BODY",
	} {
		t.Setenv(key, "")
	}
}

func TestPerformanceConfigTinyProfile(t *testing.T) {
	clearPerformanceEnv(t)
	t.Setenv("OPENTRACE_RESOURCE_PROFILE", "tiny")

	cfg, err := loadPerformanceConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GOMAXPROCS != 1 || cfg.MemoryLimitMB != 160 {
		t.Fatalf("tiny runtime budget = GOMAXPROCS %d memory %dMB", cfg.GOMAXPROCS, cfg.MemoryLimitMB)
	}
	if cfg.ColumnCacheMB != 8 || cfg.WALCacheMB != 16 || cfg.SQLiteCacheMB != 8 {
		t.Fatalf("tiny cache budget = column %dMB WAL %dMB SQLite %dMB", cfg.ColumnCacheMB, cfg.WALCacheMB, cfg.SQLiteCacheMB)
	}
	if cfg.SearchConcurrency != 1 || cfg.MaxConcurrentQueries != 2 || cfg.SealChunkEntries != 8192 {
		t.Fatalf("tiny concurrency/seal budget = %#v", cfg)
	}
	if cfg.WALCompressBody {
		t.Fatal("tiny profile should avoid transient WAL body recompression")
	}
}

func TestPerformanceConfigOverrides(t *testing.T) {
	clearPerformanceEnv(t)
	t.Setenv("OPENTRACE_RESOURCE_PROFILE", "tiny")
	t.Setenv("OPENTRACE_COLUMN_CACHE_MB", "12")
	t.Setenv("OPENTRACE_SEARCH_CONCURRENCY", "3")
	t.Setenv("OPENTRACE_ACCESS_LOG", "true")
	t.Setenv("OPENTRACE_WAL_COMPRESS_BODY", "true")

	cfg, err := loadPerformanceConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ColumnCacheMB != 12 || cfg.SearchConcurrency != 3 || !cfg.AccessLog || !cfg.WALCompressBody {
		t.Fatalf("overrides not applied: %#v", cfg)
	}
}

func TestPerformanceConfigRejectsInvalidValues(t *testing.T) {
	clearPerformanceEnv(t)
	t.Setenv("OPENTRACE_RESOURCE_PROFILE", "microscopic")
	if _, err := loadPerformanceConfig(false); err == nil {
		t.Fatal("expected invalid profile error")
	}

	t.Setenv("OPENTRACE_RESOURCE_PROFILE", "balanced")
	t.Setenv("OPENTRACE_SEAL_CHUNK_ENTRIES", "50001")
	if _, err := loadPerformanceConfig(false); err == nil {
		t.Fatal("expected invalid seal chunk size error")
	}
}
