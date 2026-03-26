package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/pkg/store"
)

// ─── Helper functions ───────────────────────────────────────────────────────

// nullString creates a sql.NullString from a Go string. Empty strings become NULL.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullInt64 creates a sql.NullInt64 from a *int64. Nil becomes NULL.
func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// nullFloat64Val creates a sql.NullFloat64 from a float64 value (always valid).
func nullFloat64Val(v float64) sql.NullFloat64 {
	return sql.NullFloat64{Float64: v, Valid: true}
}

// optionalFloat64 converts sql.NullFloat64 to *float64.
func optionalFloat64(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// optionalTime parses a sql.NullString timestamp to *time.Time.
func optionalTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t := parseTime(v.String)
	if t.IsZero() {
		return nil
	}
	return &t
}

// boolToInt64 converts a bool to an int64 (0 or 1) for SQLite boolean columns.
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// --- AgentNote converters ---

func toStoreAgentNote(row AgentNote) store.AgentNote {
	return store.AgentNote{
		ID:         row.ID,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Note:       row.Note,
		CreatedAt:  parseTime(row.CreatedAt),
		UpdatedAt:  parseTime(row.UpdatedAt),
	}
}

func toStoreAgentNotes(rows []AgentNote) []store.AgentNote {
	result := make([]store.AgentNote, len(rows))
	for i, row := range rows {
		result[i] = toStoreAgentNote(row)
	}
	return result
}

// --- AuditLog converters ---

func toStoreAuditEntry(row ListRecentAuditLogsRow) store.AuditEntry {
	return store.AuditEntry{
		ID:         row.ID,
		UserID:     row.UserID,
		UserEmail:  row.UserEmail,
		Action:     row.Action,
		TargetType: row.TargetType,
		TargetID:   row.TargetID,
		Details:    row.Details,
		IPAddress:  row.IpAddress,
		CreatedAt:  parseTime(row.CreatedAt),
	}
}

func toStoreAuditEntries(rows []ListRecentAuditLogsRow) []store.AuditEntry {
	result := make([]store.AuditEntry, len(rows))
	for i, row := range rows {
		result[i] = toStoreAuditEntry(row)
	}
	return result
}

// --- Session converters ---

func toStoreSession(row Session) *store.Session {
	return &store.Session{
		ID:        row.ID,
		UserID:    row.UserID,
		Token:     row.Token,
		ExpiresAt: parseTime(row.ExpiresAt),
		CreatedAt: parseTime(row.CreatedAt),
	}
}

// --- QueryMemory converters ---

func toStoreQueryMemory(row QueryMemory) *store.QueryMemory {
	qm := &store.QueryMemory{
		Fingerprint:                row.Fingerprint,
		LastInvestigationSessionID: row.LastInvestigationSessionID,
		InvestigationCount:         int(row.InvestigationCount),
		LastRootCause:              row.LastRootCause,
		LastFix:                    row.LastFix,
		FirstSeenAt:                parseTime(row.FirstSeenAt),
		LastSeenAt:                 parseTime(row.LastSeenAt),
	}
	if row.AvgDurationBeforeMs.Valid {
		v := int(row.AvgDurationBeforeMs.Int64)
		qm.AvgDurationBeforeMs = &v
	}
	if row.AvgDurationAfterMs.Valid {
		v := int(row.AvgDurationAfterMs.Int64)
		qm.AvgDurationAfterMs = &v
	}
	return qm
}

// --- RunbookEffectiveness converters ---

func toStoreRunbookEffectiveness(row RunbookEffectiveness) store.RunbookEffectiveness {
	return store.RunbookEffectiveness{
		RunbookName:               row.RunbookName,
		TotalExecutions:           int(row.TotalExecutions),
		ResolvedSessions:          int(row.ResolvedSessions),
		AbandonedSessions:         int(row.AbandonedSessions),
		AvgStepsAfter:             int(row.AvgStepsAfter),
		AvgSessionDurationSeconds: int(row.AvgSessionDurationSeconds),
		LastExecutedAt:            parseTime(row.LastExecutedAt),
	}
}

func toStoreRunbookEffectivenessList(rows []RunbookEffectiveness) []store.RunbookEffectiveness {
	result := make([]store.RunbookEffectiveness, len(rows))
	for i, row := range rows {
		result[i] = toStoreRunbookEffectiveness(row)
	}
	return result
}

// --- ToolTransition converters ---

func toStoreToolTransition(row ToolTransition) store.ToolTransition {
	return store.ToolTransition{
		FromTool:       row.FromTool,
		ToTool:         row.ToTool,
		Intent:         row.Intent,
		TotalCount:     int(row.TotalCount),
		ResolvedCount:  int(row.ResolvedCount),
		AbandonedCount: int(row.AbandonedCount),
		AvgDurationMs:  int(row.AvgDurationMs),
		LastSeenAt:     parseTime(row.LastSeenAt),
	}
}

// --- WorkflowTemplate converters ---

func toStoreWorkflowTemplate(row WorkflowTemplate) store.WorkflowTemplate {
	return store.WorkflowTemplate{
		ID:                   int(row.ID),
		Intent:               row.Intent,
		Name:                 row.Name,
		StepOrder:            int(row.StepOrder),
		ToolName:             row.ToolName,
		ArgsHint:             row.ArgsHint,
		Source:               row.Source,
		ResolvedSessionCount: int(row.ResolvedSessionCount),
		CreatedAt:            parseTime(row.CreatedAt),
	}
}

func toStoreWorkflowTemplates(rows []WorkflowTemplate) []store.WorkflowTemplate {
	result := make([]store.WorkflowTemplate, len(rows))
	for i, row := range rows {
		result[i] = toStoreWorkflowTemplate(row)
	}
	return result
}

// ─── Deploy converters ──────────────────────────────────────────────────────

func toStoreDeploy(d Deploy) store.Deploy {
	sd := store.Deploy{
		ID:           d.ID,
		Service:      d.Service,
		Environment:  d.Environment,
		CommitHash:   d.CommitHash,
		Branch:       d.Branch,
		Author:       d.Author,
		DeploySource: store.DeploySource(d.DeploySource),
		Status:       store.DeployStatus(d.Status),
		DeployedAt:   parseTime(d.DeployedAt),
		CreatedAt:    parseTime(d.CreatedAt),
	}
	if err := json.Unmarshal([]byte(d.FilesChangedJson), &sd.FilesChanged); err != nil {
		slog.Warn("deploy: malformed files_changed_json", "id", d.ID, "error", err)
	}
	if err := json.Unmarshal([]byte(d.LinkedInvestigationIdsJson), &sd.LinkedInvestigationIDs); err != nil {
		slog.Warn("deploy: malformed linked_investigation_ids_json", "id", d.ID, "error", err)
	}
	sd.PreErrorRate = optionalFloat64(d.PreErrorRate)
	sd.PostErrorRate = optionalFloat64(d.PostErrorRate)
	sd.PreAvgDurationMs = optionalFloat64(d.PreAvgDurationMs)
	sd.PostAvgDurationMs = optionalFloat64(d.PostAvgDurationMs)
	sd.ImpactMeasuredAt = optionalTime(d.ImpactMeasuredAt)
	return sd
}

func toStoreDeploys(rows []Deploy) []store.Deploy {
	result := make([]store.Deploy, len(rows))
	for i, r := range rows {
		result[i] = toStoreDeploy(r)
	}
	return result
}

// ─── Event converters ───────────────────────────────────────────────────────

func toStoreEvent(e Event) store.Event {
	se := store.Event{
		ID:          e.ID,
		EventType:   store.EventType(e.EventType),
		Source:      e.Source,
		Service:     e.Service,
		Title:       e.Title,
		Description: e.Description,
		ExternalID:  e.ExternalID,
		ExternalURL: e.ExternalUrl,
		Author:      e.Author,
		CreatedAt:   parseTime(e.CreatedAt),
	}
	if e.MetadataJson != "" && e.MetadataJson != "{}" {
		if err := json.Unmarshal([]byte(e.MetadataJson), &se.Metadata); err != nil {
			slog.Warn("event: malformed metadata_json", "id", e.ID, "error", err)
		}
	}
	return se
}

// ─── CodeEntity converters ──────────────────────────────────────────────────

func toStoreCodeEntity(e CodeEntity) store.CodeEntity {
	se := store.CodeEntity{
		ID:                  e.ID,
		EntityType:          store.CodeEntityType(e.EntityType),
		EntityName:          e.EntityName,
		Service:             e.Service,
		RiskScore:           e.RiskScore,
		ErrorCount:          int(e.ErrorCount),
		InvestigationCount:  int(e.InvestigationCount),
		AvgDurationMs:       optionalFloat64(e.AvgDurationMs),
		LastErrorAt:         optionalTime(e.LastErrorAt),
		LastInvestigationAt: optionalTime(e.LastInvestigationAt),
		CreatedAt:           parseTime(e.CreatedAt),
		UpdatedAt:           parseTime(e.UpdatedAt),
	}
	if e.MetadataJson != "" && e.MetadataJson != "{}" {
		if err := json.Unmarshal([]byte(e.MetadataJson), &se.Metadata); err != nil {
			slog.Warn("code_entity: malformed metadata_json", "entity", e.EntityName, "error", err)
		}
	}
	return se
}

func toStoreCodeEntities(rows []CodeEntity) []store.CodeEntity {
	result := make([]store.CodeEntity, len(rows))
	for i, r := range rows {
		result[i] = toStoreCodeEntity(r)
	}
	return result
}

// ─── TraceStatus converters ─────────────────────────────────────────────────

func toStoreTraceStatus(t TraceStatus) store.TraceStatus {
	st := store.TraceStatus{
		TraceID:       t.TraceID,
		SpanCount:     int(t.SpanCount),
		DurationMs:    t.DurationMs,
		Status:        t.Status,
		HasErrors:     t.HasErrors != 0,
		FirstSeenAt:   parseTime(t.FirstSeenAt),
		LastUpdatedAt: parseTime(t.LastUpdatedAt),
	}
	if t.RootSpanID.Valid {
		st.RootSpanID = t.RootSpanID.String
	}
	if err := json.Unmarshal([]byte(t.Services), &st.Services); err != nil {
		st.Services = []string{}
	}
	return st
}

func toStoreTraceStatuses(rows []TraceStatus) []store.TraceStatus {
	result := make([]store.TraceStatus, len(rows))
	for i, r := range rows {
		result[i] = toStoreTraceStatus(r)
	}
	return result
}

// ─── UncoveredErrorPath converters ──────────────────────────────────────────

func toStoreUncoveredPath(p UncoveredErrorPath) store.UncoveredErrorPath {
	return store.UncoveredErrorPath{
		ID:                 p.ID,
		Service:            p.Service,
		ErrorFingerprint:   p.ErrorFingerprint,
		ErrorClass:         p.ErrorClass,
		SourceFile:         p.SourceFile,
		Endpoint:           p.Endpoint,
		ErrorCount:         int(p.ErrorCount),
		UserImpactScore:    p.UserImpactScore,
		InvestigationCount: int(p.InvestigationCount),
		PriorityScore:      p.PriorityScore,
		LastSeenAt:         parseTime(p.LastSeenAt),
		CreatedAt:          parseTime(p.CreatedAt),
		UpdatedAt:          parseTime(p.UpdatedAt),
	}
}

func toStoreUncoveredPaths(rows []UncoveredErrorPath) []store.UncoveredErrorPath {
	result := make([]store.UncoveredErrorPath, len(rows))
	for i, r := range rows {
		result[i] = toStoreUncoveredPath(r)
	}
	return result
}

// ─── Server converters ──────────────────────────────────────────────────────

func toStoreServerFromRow(r GetServerByIDRow) store.Server {
	srv := store.Server{
		Hostname:     r.Hostname,
		DisplayName:  r.DisplayName.String,
		IPAddress:    r.IpAddress.String,
		OS:           r.Os.String,
		Arch:         r.Arch.String,
		AgentVersion: r.AgentVersion.String,
		Status:       store.ServerStatus(r.Status),
		CreatedAt:    parseTime(r.CreatedAt),
		UpdatedAt:    parseTime(r.UpdatedAt),
	}
	srv.ID, _ = uuid.Parse(r.ID)
	srv.LastSeenAt = optionalTime(r.LastSeenAt)
	if r.Labels.Valid && r.Labels.String != "" {
		if err := json.Unmarshal([]byte(r.Labels.String), &srv.Labels); err != nil {
			slog.Warn("invalid labels JSON", "server_id", r.ID, "error", err)
		}
	}
	return srv
}

func toStoreServerFromListRow(r ListServersRow) store.Server {
	srv := store.Server{
		Hostname:     r.Hostname,
		DisplayName:  r.DisplayName.String,
		IPAddress:    r.IpAddress.String,
		OS:           r.Os.String,
		Arch:         r.Arch.String,
		AgentVersion: r.AgentVersion.String,
		Status:       store.ServerStatus(r.Status),
		CreatedAt:    parseTime(r.CreatedAt),
		UpdatedAt:    parseTime(r.UpdatedAt),
	}
	srv.ID, _ = uuid.Parse(r.ID)
	srv.LastSeenAt = optionalTime(r.LastSeenAt)
	if r.Labels.Valid && r.Labels.String != "" {
		if err := json.Unmarshal([]byte(r.Labels.String), &srv.Labels); err != nil {
			slog.Warn("invalid labels JSON", "server_id", r.ID, "error", err)
		}
	}
	return srv
}

func toStoreServers(rows []ListServersRow) []store.Server {
	result := make([]store.Server, len(rows))
	for i, r := range rows {
		result[i] = toStoreServerFromListRow(r)
	}
	return result
}

// ─── Metric converters ──────────────────────────────────────────────────────

func toStoreMetricPoint(r GetLatestMetricsByServerRow) store.MetricPoint {
	mp := store.MetricPoint{
		ID:          r.ID,
		Timestamp:   parseTime(r.Timestamp),
		MetricName:  r.MetricName,
		MetricValue: r.MetricValue,
	}
	mp.ServerID, _ = uuid.Parse(r.ServerID)
	if r.Unit.Valid {
		mp.Unit = r.Unit.String
	}
	if r.Labels.Valid && r.Labels.String != "" && r.Labels.String != "{}" {
		if err := json.Unmarshal([]byte(r.Labels.String), &mp.Labels); err != nil {
			slog.Warn("invalid labels JSON in metric", "metric", r.MetricName, "error", err)
			mp.Labels = make(map[string]string)
		}
	}
	return mp
}

func toStoreMetricPoints(rows []GetLatestMetricsByServerRow) []store.MetricPoint {
	result := make([]store.MetricPoint, len(rows))
	for i, r := range rows {
		result[i] = toStoreMetricPoint(r)
	}
	return result
}

// ─── DataSource converters ──────────────────────────────────────────────────

func toStoreDataSourceFromRow(r GetDataSourceByIDRow) (store.DataSource, error) {
	ds := store.DataSource{
		Type:      store.ConnectorType(r.Type),
		Name:      r.Name,
		Status:    store.ConnectorStatus(r.Status),
		CreatedAt: parseTime(r.CreatedAt),
		UpdatedAt: parseTime(r.UpdatedAt),
	}
	ds.ID, _ = uuid.Parse(r.ID)
	if r.StatusMessage.Valid {
		ds.StatusMessage = &r.StatusMessage.String
	}
	ds.LastTestedAt = optionalTime(r.LastTestedAt)
	if err := json.Unmarshal([]byte(r.Config), &ds.Config); err != nil {
		return ds, fmt.Errorf("unmarshaling config: %w", err)
	}
	return ds, nil
}

func toStoreDataSourceFromListRow(r ListAllDataSourcesRow) (store.DataSource, error) {
	ds := store.DataSource{
		Type:      store.ConnectorType(r.Type),
		Name:      r.Name,
		Status:    store.ConnectorStatus(r.Status),
		CreatedAt: parseTime(r.CreatedAt),
		UpdatedAt: parseTime(r.UpdatedAt),
	}
	ds.ID, _ = uuid.Parse(r.ID)
	if r.StatusMessage.Valid {
		ds.StatusMessage = &r.StatusMessage.String
	}
	ds.LastTestedAt = optionalTime(r.LastTestedAt)
	if err := json.Unmarshal([]byte(r.Config), &ds.Config); err != nil {
		return ds, fmt.Errorf("unmarshaling config: %w", err)
	}
	return ds, nil
}

func toStoreDataSourceFromTypeRow(r ListDataSourcesByTypeRow) (store.DataSource, error) {
	ds := store.DataSource{
		Type:      store.ConnectorType(r.Type),
		Name:      r.Name,
		Status:    store.ConnectorStatus(r.Status),
		CreatedAt: parseTime(r.CreatedAt),
		UpdatedAt: parseTime(r.UpdatedAt),
	}
	ds.ID, _ = uuid.Parse(r.ID)
	if r.StatusMessage.Valid {
		ds.StatusMessage = &r.StatusMessage.String
	}
	ds.LastTestedAt = optionalTime(r.LastTestedAt)
	if err := json.Unmarshal([]byte(r.Config), &ds.Config); err != nil {
		return ds, fmt.Errorf("unmarshaling config: %w", err)
	}
	return ds, nil
}

// ─── MCP Activity converters ────────────────────────────────────────────────

func toStoreMCPActivityEvent(r ListRecentMCPActivityRow) store.MCPActivityEvent {
	e := store.MCPActivityEvent{
		ID:            r.ID,
		SessionID:     r.SessionID,
		UserID:        r.UserID,
		ToolName:      r.ToolName,
		Arguments:     r.Arguments,
		ResultPreview: r.ResultPreview,
		IsError:       r.IsError != 0,
		EventType:     r.EventType,
		CreatedAt:     parseTime(r.CreatedAt),
	}
	if r.DurationMs.Valid {
		e.DurationMs = &r.DurationMs.Int64
	}
	return e
}

func toStoreMCPActivityEvents(rows []ListRecentMCPActivityRow) []store.MCPActivityEvent {
	result := make([]store.MCPActivityEvent, len(rows))
	for i, r := range rows {
		result[i] = toStoreMCPActivityEvent(r)
	}
	return result
}

func toStoreMCPActivityEventFromInvRow(r ListMCPActivityByInvestigationSessionRow) store.MCPActivityEvent {
	e := store.MCPActivityEvent{
		ID:            r.ID,
		SessionID:     r.SessionID,
		UserID:        r.UserID,
		ToolName:      r.ToolName,
		Arguments:     r.Arguments,
		ResultPreview: r.ResultPreview,
		IsError:       r.IsError != 0,
		EventType:     r.EventType,
		CreatedAt:     parseTime(r.CreatedAt),
	}
	if r.DurationMs.Valid {
		e.DurationMs = &r.DurationMs.Int64
	}
	return e
}

func toStoreMCPActivityEventsFromInvRows(rows []ListMCPActivityByInvestigationSessionRow) []store.MCPActivityEvent {
	result := make([]store.MCPActivityEvent, len(rows))
	for i, r := range rows {
		result[i] = toStoreMCPActivityEventFromInvRow(r)
	}
	return result
}

// ─── HealthCheck converters ──────────────────────────────────────────────────

func healthcheckRowToStore(row GetHealthcheckRow) *store.HealthCheck {
	return &store.HealthCheck{
		ID:             row.ID,
		Name:           row.Name,
		URL:            row.Url,
		Method:         row.Method,
		IntervalSecs:   int(row.IntervalSecs),
		TimeoutSecs:    int(row.TimeoutSecs),
		ExpectedStatus: int(row.ExpectedStatus),
		ExpectedBody:   row.ExpectedBody,
		Retries:        int(row.Retries),
		Enabled:        row.Enabled != 0,
		CreatedAt:      parseTime(row.CreatedAt),
	}
}

func healthcheckListRowToStore(row ListHealthchecksRow) store.HealthCheck {
	return store.HealthCheck{
		ID:             row.ID,
		Name:           row.Name,
		URL:            row.Url,
		Method:         row.Method,
		IntervalSecs:   int(row.IntervalSecs),
		TimeoutSecs:    int(row.TimeoutSecs),
		ExpectedStatus: int(row.ExpectedStatus),
		ExpectedBody:   row.ExpectedBody,
		Retries:        int(row.Retries),
		Enabled:        row.Enabled != 0,
		CreatedAt:      parseTime(row.CreatedAt),
	}
}

func healthcheckResultToStore(row HealthcheckResult) store.HealthCheckResult {
	r := store.HealthCheckResult{
		ID:            row.ID,
		HealthCheckID: row.HealthcheckID,
		Status:        store.HealthCheckStatus(row.Status),
		Error:         row.Error,
		CheckedAt:     parseTime(row.CheckedAt),
	}
	if row.StatusCode.Valid {
		v := int(row.StatusCode.Int64)
		r.StatusCode = &v
	}
	if row.ResponseMs.Valid {
		v := int(row.ResponseMs.Int64)
		r.ResponseMs = &v
	}
	return r
}

func healthcheckResultsToStore(rows []HealthcheckResult) []store.HealthCheckResult {
	result := make([]store.HealthCheckResult, len(rows))
	for i, row := range rows {
		result[i] = healthcheckResultToStore(row)
	}
	return result
}

// ─── ErrorGroup converters ───────────────────────────────────────────────────

func errorGroupToStore(row ErrorGroup) *store.ErrorGroup {
	eg := &store.ErrorGroup{
		Fingerprint:     row.Fingerprint,
		Service:         row.Service,
		Environment:     row.Environment,
		ExceptionClass:  row.ExceptionClass,
		Message:         row.Message,
		SourceFile:      row.SourceFile,
		SourceLine:      int(row.SourceLine),
		Status:          store.ErrorGroupStatus(row.Status),
		FirstSeenAt:     parseTime(row.FirstSeenAt),
		LastSeenAt:      parseTime(row.LastSeenAt),
		OccurrenceCount: int(row.OccurrenceCount),
		ReopenedCount:   int(row.ReopenedCount),
		UniqueUsers:     int(row.UniqueUsers),
		ImpactScore:     row.ImpactScore,
	}
	if row.LastLogID.Valid {
		eg.LastLogID = &row.LastLogID.Int64
	}
	if row.ResolvedAt.Valid {
		t := parseTime(row.ResolvedAt.String)
		eg.ResolvedAt = &t
	}
	if row.IgnoredAt.Valid {
		t := parseTime(row.IgnoredAt.String)
		eg.IgnoredAt = &t
	}
	if row.CommonContext != "" && row.CommonContext != "{}" {
		json.Unmarshal([]byte(row.CommonContext), &eg.CommonContext)
	}
	return eg
}

func errorGroupEventToStore(row ErrorGroupEvent) store.ErrorGroupEvent {
	return store.ErrorGroupEvent{
		ID:          row.ID,
		Fingerprint: row.Fingerprint,
		Action:      row.Action,
		Reason:      row.Reason,
		CreatedAt:   parseTime(row.CreatedAt),
	}
}

func errorGroupEventsToStore(rows []ErrorGroupEvent) []store.ErrorGroupEvent {
	result := make([]store.ErrorGroupEvent, len(rows))
	for i, row := range rows {
		result[i] = errorGroupEventToStore(row)
	}
	return result
}

// ─── ErrorImpact converters ──────────────────────────────────────────────────

func affectedUserRowToStore(row ListAffectedUsersRow) store.AffectedUser {
	au := store.AffectedUser{
		UserID:          row.UserID,
		OccurrenceCount: int(row.OccurrenceCount),
		FirstSeenAt:     parseTime(row.FirstSeenAt),
		LastSeenAt:      parseTime(row.LastSeenAt),
	}
	if row.LastContext != "" && row.LastContext != "{}" {
		json.Unmarshal([]byte(row.LastContext), &au.LastContext)
	}
	return au
}

func affectedUsersToStore(rows []ListAffectedUsersRow) []store.AffectedUser {
	result := make([]store.AffectedUser, len(rows))
	for i, row := range rows {
		result[i] = affectedUserRowToStore(row)
	}
	return result
}

// ─── Watch converters ────────────────────────────────────────────────────────

func watchToStore(row Watch) *store.Watch {
	w := &store.Watch{
		ID:                  row.ID,
		Metric:              store.WatchMetric(row.Metric),
		Operator:            store.WatchOperator(row.Operator),
		Threshold:           row.Threshold,
		Service:             row.Service,
		Endpoint:            row.Endpoint,
		Environment:         row.Environment,
		CommitHash:          row.CommitHash,
		Duration:            row.Duration,
		Urgency:             store.WatchUrgency(row.Urgency),
		CheckInterval:       row.CheckInterval,
		BaselineWindow:      row.BaselineWindow,
		MinConsecutive:      int(row.MinConsecutive),
		Status:              store.WatchStatus(row.Status),
		ConsecutiveBreaches: int(row.ConsecutiveBreaches),
		CreatedBy:           row.CreatedBy,
		SessionID:           row.SessionID,
		CreatedAt:           parseTime(row.CreatedAt),
		UpdatedAt:           parseTime(row.UpdatedAt),
	}
	if row.BaselineJson.Valid {
		var b store.WatchBaseline
		if json.Unmarshal([]byte(row.BaselineJson.String), &b) == nil {
			w.BaselineJSON = &b
		}
	}
	if row.CurrentValue.Valid {
		w.CurrentValue = &row.CurrentValue.Float64
	}
	if row.ExpiresAt.Valid {
		t := parseTime(row.ExpiresAt.String)
		w.ExpiresAt = &t
	}
	if row.LastCheckedAt.Valid {
		t := parseTime(row.LastCheckedAt.String)
		w.LastCheckedAt = &t
	}
	if row.NextCheckAt.Valid {
		t := parseTime(row.NextCheckAt.String)
		w.NextCheckAt = &t
	}
	return w
}

func watchesToStore(rows []Watch) []store.Watch {
	result := make([]store.Watch, len(rows))
	for i, row := range rows {
		result[i] = *watchToStore(row)
	}
	return result
}

func watchRunToStore(row WatchRun) *store.WatchRun {
	r := &store.WatchRun{
		ID:        row.ID,
		WatchID:   row.WatchID,
		Status:    row.Status,
		Breached:  row.Breached != 0,
		StartedAt: parseTime(row.StartedAt),
	}
	if row.MetricValue.Valid {
		r.MetricValue = &row.MetricValue.Float64
	}
	if row.Summary.Valid {
		r.Summary = row.Summary.String
	}
	if row.ErrorMessage.Valid {
		r.ErrorMessage = row.ErrorMessage.String
	}
	if row.FinishedAt.Valid {
		t := parseTime(row.FinishedAt.String)
		r.FinishedAt = &t
	}
	return r
}

func watchRunsToStore(rows []WatchRun) []store.WatchRun {
	result := make([]store.WatchRun, len(rows))
	for i, row := range rows {
		result[i] = *watchRunToStore(row)
	}
	return result
}

func watchAlertToStore(row WatchAlert) *store.WatchAlert {
	a := &store.WatchAlert{
		ID:             row.ID,
		WatchID:        row.WatchID,
		Urgency:        store.WatchUrgency(row.Urgency),
		Summary:        row.Summary,
		TriggerMetric:  row.TriggerMetric,
		TriggerValue:   row.TriggerValue,
		ThresholdValue: row.ThresholdValue,
		Status:         row.Status,
		CreatedAt:      parseTime(row.CreatedAt),
	}
	if row.RunID.Valid {
		a.RunID = row.RunID.String
	}
	if row.EvidenceJson.Valid {
		var ev store.WatchEvidenceBundle
		if json.Unmarshal([]byte(row.EvidenceJson.String), &ev) == nil {
			a.EvidenceJSON = &ev
		}
	}
	if row.DismissReason.Valid {
		a.DismissReason = row.DismissReason.String
	}
	return a
}

// ─── User converters ─────────────────────────────────────────────────────────

func userToStore(row User) *store.User {
	u := &store.User{
		ID:           row.ID,
		Email:        row.Email,
		PasswordHash: row.PasswordHash,
		DisplayName:  row.DisplayName,
		Role:         store.UserRole(row.Role),
		MCPEnabled:   row.McpEnabled != 0,
		IsActive:     row.IsActive != 0,
		CreatedAt:    parseTime(row.CreatedAt),
		UpdatedAt:    parseTime(row.UpdatedAt),
	}
	if row.McpToken.Valid {
		u.MCPToken = &row.McpToken.String
	}
	return u
}

func usersToStore(rows []User) []store.User {
	result := make([]store.User, len(rows))
	for i, row := range rows {
		result[i] = *userToStore(row)
	}
	return result
}

// ─── Log converters ──────────────────────────────────────────────────────────

func logRowToStore(row GetLogByIDRow) *store.LogEntry {
	entry := &store.LogEntry{
		ID:        row.ID,
		Timestamp: parseTimeNano(row.Timestamp),
		Level:     row.Level,
		Service:   row.Service,
		Message:   row.Message,
		EventType: row.EventType,
	}
	if row.Environment.Valid {
		entry.Environment = row.Environment.String
	}
	if row.CommitHash.Valid {
		entry.CommitHash = row.CommitHash.String
	}
	if row.TraceID.Valid {
		entry.TraceID = row.TraceID.String
	}
	if row.SpanID.Valid {
		entry.SpanID = row.SpanID.String
	}
	if row.ParentSpanID.Valid {
		entry.ParentSpanID = row.ParentSpanID.String
	}
	if row.RequestID.Valid {
		entry.RequestID = row.RequestID.String
	}
	if row.UserID.Valid {
		entry.UserID = row.UserID.String
	}
	if row.SessionID.Valid {
		entry.SessionID = row.SessionID.String
	}
	if row.ExceptionClass.Valid {
		entry.ExceptionClass = row.ExceptionClass.String
	}
	if row.ErrorFingerprint.Valid {
		entry.ErrorFingerprint = row.ErrorFingerprint.String
	}
	if row.SourceFile.Valid {
		entry.SourceFile = row.SourceFile.String
	}
	if row.SourceLine.Valid {
		entry.SourceLine = int(row.SourceLine.Int64)
	}
	if row.Metadata.Valid && row.Metadata.String != "" {
		json.Unmarshal([]byte(row.Metadata.String), &entry.Metadata)
	}
	return entry
}

func batchRowToStore(row IngestBatch) *store.BatchRecord {
	return &store.BatchRecord{
		BatchID:    row.BatchID,
		LogCount:   int(row.LogCount),
		ReceivedAt: parseTimeNano(row.ReceivedAt),
	}
}

// ─── Journey converters ──────────────────────────────────────────────────────

func journeySessionToStore(row UserSession) *store.UserSession {
	return &store.UserSession{
		ID:              row.ID,
		SessionID:       row.SessionID,
		UserID:          row.UserID,
		Service:         row.Service,
		Environment:     row.Environment,
		StartedAt:       parseTime(row.StartedAt),
		EndedAt:         parseTime(row.EndedAt),
		RequestCount:    int(row.RequestCount),
		ErrorCount:      int(row.ErrorCount),
		TotalDurationMs: row.TotalDurationMs,
		EntryPath:       row.EntryPath,
		ExitPath:        row.ExitPath,
		ExitStatus:      int(row.ExitStatus),
		HasError:        row.HasError != 0,
		CreatedAt:       parseTime(row.CreatedAt),
	}
}

func journeyFunnelToStore(row Funnel) *store.Funnel {
	f := &store.Funnel{
		ID:        row.ID,
		Name:      row.Name,
		Service:   row.Service,
		CreatedAt: parseTime(row.CreatedAt),
		UpdatedAt: parseTime(row.UpdatedAt),
	}
	json.Unmarshal([]byte(row.Steps), &f.Steps)
	return f
}

func journeyFunnelsToStore(rows []Funnel) []store.Funnel {
	result := make([]store.Funnel, len(rows))
	for i, row := range rows {
		result[i] = *journeyFunnelToStore(row)
	}
	return result
}

func journeyRequestStepFromRow(row GetSessionRequestsRow) store.RequestStep {
	return store.RequestStep{
		Timestamp:  parseTimeNano(row.Timestamp),
		Controller: row.Controller,
		Action:     row.Action,
		Path:       row.Path,
		Method:     row.Method,
		Status:     int(row.Status),
		DurationMs: row.DurationMs,
		SQLCount:   int(row.SqlCount),
		HasError:   row.HasError != 0,
		ErrorClass: row.ExceptionClass,
		RequestID:  row.RequestID,
		LogID:      row.LogID,
	}
}

func journeyRequestStepsToStore(rows []GetSessionRequestsRow) []store.RequestStep {
	result := make([]store.RequestStep, len(rows))
	for i, row := range rows {
		result[i] = journeyRequestStepFromRow(row)
	}
	return result
}

func journeyRequestTimelineFromRow(row GetRequestTimelineRow) *store.RequestTimeline {
	rt := &store.RequestTimeline{
		LogID:      row.LogID,
		RequestID:  row.RequestID,
		Controller: row.Controller,
		Action:     row.Action,
		Path:       row.Path,
		Status:     int(row.Status),
		DurationMs: row.DurationMs,
		StartedAt:  parseTimeNano(row.Timestamp),
	}
	if row.Timeline.Valid && row.Timeline.String != "" {
		rt.Events = parseTimelineJSON(row.Timeline.String)
	}
	if len(rt.Events) > 0 {
		var slowest *store.TimelineEvent
		for i := range rt.Events {
			if slowest == nil || rt.Events[i].DurationMs > slowest.DurationMs {
				slowest = &rt.Events[i]
			}
		}
		rt.Bottleneck = slowest
	}
	return rt
}

func journeySessionTimelineFromRow(row GetSessionTimelineRow) store.RequestTimeline {
	rt := store.RequestTimeline{
		LogID:      row.LogID,
		RequestID:  row.RequestID,
		Controller: row.Controller,
		Action:     row.Action,
		Path:       row.Path,
		Status:     int(row.Status),
		DurationMs: row.DurationMs,
		StartedAt:  parseTimeNano(row.Timestamp),
	}
	if row.Timeline.Valid && row.Timeline.String != "" {
		rt.Events = parseTimelineJSON(row.Timeline.String)
	}
	if len(rt.Events) > 0 {
		var slowest *store.TimelineEvent
		for i := range rt.Events {
			if slowest == nil || rt.Events[i].DurationMs > slowest.DurationMs {
				slowest = &rt.Events[i]
			}
		}
		rt.Bottleneck = slowest
	}
	return rt
}

// ─── Trend converters ────────────────────────────────────────────────────────

func deployMarkerToStore(row DeployMarker) store.DeployMarker {
	return store.DeployMarker{
		ID:           row.ID,
		Service:      row.Service,
		Environment:  row.Environment,
		CommitHash:   row.CommitHash,
		FirstSeenAt:  parseTime(row.FirstSeenAt),
		RequestCount: int(row.RequestCount),
	}
}

func deployMarkersToStore(rows []DeployMarker) []store.DeployMarker {
	result := make([]store.DeployMarker, len(rows))
	for i, row := range rows {
		result[i] = deployMarkerToStore(row)
	}
	return result
}

// ─── Misc helpers ────────────────────────────────────────────────────────────

// parseTimeNano parses an RFC3339Nano timestamp, falling back to RFC3339.
func parseTimeNano(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return parseTime(s)
	}
	return t
}
