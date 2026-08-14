package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------- fakes ----------

// fakeLogStore embeds the interface so only the methods under test need an
// implementation; anything else would panic loudly rather than silently pass.
type fakeLogStore struct {
	store.LogStore
	entry *store.LogEntry
	err   error
}

func (f *fakeLogStore) GetByID(context.Context, int64) (*store.LogEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entry, nil
}

func (f *fakeLogStore) Histogram(context.Context, store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

type fakeErrorGroupStore struct {
	store.ErrorGroupStore
	gotParams store.ListErrorGroupParams
}

func (f *fakeErrorGroupStore) List(_ context.Context, p store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	f.gotParams = p
	return nil, nil
}

// pagingWatchStore serves a fixed population of watches through Limit/Offset.
type pagingWatchStore struct {
	store.WatchStore
	all      []store.Watch
	maxLimit int
}

func (p *pagingWatchStore) List(_ context.Context, params store.ListWatchParams) ([]store.Watch, error) {
	limit := params.Limit
	if p.maxLimit > 0 && (limit == 0 || limit > p.maxLimit) {
		limit = p.maxLimit
	}
	if params.Offset >= len(p.all) {
		return nil, nil
	}
	end := params.Offset + limit
	if end > len(p.all) {
		end = len(p.all)
	}
	return p.all[params.Offset:end], nil
}

func newHandler(deps *server.Deps) *handler { return &handler{deps: deps} }

func doRequest(h http.HandlerFunc, target string, urlParams map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if len(urlParams) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range urlParams {
			rctx.URLParams.Add(k, v)
		}
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// ---------- handleLogDetail ----------

// TestHandleLogDetail_StoreErrorIs500 pins the ErrNotFound sentinel
// convention: a read failure must not be reported as "log not found".
func TestHandleLogDetail_StoreErrorIs500(t *testing.T) {
	h := newHandler(&server.Deps{Stores: store.Stores{
		LogStore: &fakeLogStore{err: errors.New("open chunk for ID 7: input/output error")},
	}})

	rec := doRequest(h.handleLogDetail, "/logs/7", map[string]string{"id": "7"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for a store failure, got %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "input/output error") || strings.Contains(body, "chunk") {
		t.Fatalf("internal error detail leaked: %s", body)
	}
}

func TestHandleLogDetail_NotFoundSentinelIs404(t *testing.T) {
	h := newHandler(&server.Deps{Stores: store.Stores{
		LogStore: &fakeLogStore{err: store.ErrNotFound},
	}})

	rec := doRequest(h.handleLogDetail, "/logs/7", map[string]string{"id": "7"})

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for the ErrNotFound sentinel, got %d", rec.Code)
	}
}

func TestHandleLogDetail_Found(t *testing.T) {
	h := newHandler(&server.Deps{Stores: store.Stores{
		LogStore: &fakeLogStore{entry: &store.LogEntry{ID: 7}},
	}})

	rec := doRequest(h.handleLogDetail, "/logs/7", map[string]string{"id": "7"})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- 'since' validation ----------

// TestHandleErrorsTop_InvalidSinceIs400 matches /logs/tail: a malformed window
// must not silently become "the last hour".
func TestHandleErrorsTop_InvalidSinceIs400(t *testing.T) {
	egs := &fakeErrorGroupStore{}
	h := newHandler(&server.Deps{Stores: store.Stores{ErrorGroupStore: egs}})

	rec := doRequest(h.handleErrorsTop, "/errors/top?since=2026-08-01T00:00", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed 'since', got %d", rec.Code)
	}
}

func TestHandleErrorsTop_ValidSinceApplied(t *testing.T) {
	egs := &fakeErrorGroupStore{}
	h := newHandler(&server.Deps{Stores: store.Stores{ErrorGroupStore: egs}})

	rec := doRequest(h.handleErrorsTop, "/errors/top?since=24h", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if egs.gotParams.ActiveSince == nil {
		t.Fatal("since was not passed to the store")
	}
}

func TestHandleIngestionStats_InvalidSinceIs400(t *testing.T) {
	h := newHandler(&server.Deps{Stores: store.Stores{LogStore: &fakeLogStore{}}})

	rec := doRequest(h.handleIngestionStats, "/ingestion/stats?since=yesterday", nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed 'since', got %d", rec.Code)
	}
}

func TestHandleIngestionStats_DefaultSince(t *testing.T) {
	h := newHandler(&server.Deps{Stores: store.Stores{LogStore: &fakeLogStore{}}})

	rec := doRequest(h.handleIngestionStats, "/ingestion/stats", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

// ---------- watch tally ----------

// TestCountWatchesByStatus_PagesBeyondFirstPage proves the status counters no
// longer stop at the first page: triggered watches past the page boundary were
// previously invisible.
func TestCountWatchesByStatus_PagesBeyondFirstPage(t *testing.T) {
	var all []store.Watch
	for i := 0; i < watchCountPageSize+300; i++ {
		s := store.WatchStatusActive
		if i >= watchCountPageSize {
			s = store.WatchStatusTriggered
		}
		all = append(all, store.Watch{ID: fmt.Sprint(i), Status: s})
	}
	ws := &pagingWatchStore{all: all, maxLimit: watchCountPageSize}

	got := countWatchesByStatus(context.Background(), ws)

	if got.Active != watchCountPageSize {
		t.Errorf("active = %d, want %d", got.Active, watchCountPageSize)
	}
	if got.Triggered != 300 {
		t.Errorf("triggered = %d, want 300 (watches past the first page were dropped)", got.Triggered)
	}
}

func TestCountWatchesByStatus_SinglePartialPage(t *testing.T) {
	ws := &pagingWatchStore{all: []store.Watch{
		{Status: store.WatchStatusActive},
		{Status: store.WatchStatusResolved},
		{Status: store.WatchStatusExpired},
	}, maxLimit: watchCountPageSize}

	got := countWatchesByStatus(context.Background(), ws)
	if got.Active != 1 || got.Resolved != 1 || got.Expired != 1 {
		t.Fatalf("unexpected tally: %+v", got)
	}
}
