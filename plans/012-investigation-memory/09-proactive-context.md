# Proactive Context Delivery

Don't wait for the agent to call a tool. Push relevant production context automatically via MCP notifications and resources.

## MCP Notifications (Server → Client)

```go
// When something production-relevant happens while an agent is connected
func (s *MCPServer) checkForProactiveAlerts(ctx context.Context) {
    if s.session == nil || s.session.Intent == "" {
        return
    }

    // 1. Did any watcher fire for the service the agent is working on?
    if s.session.PrimaryService != "" {
        alerts, _ := s.watchStore.ListAlerts(ctx, store.ListAlertParams{
            Service:  s.session.PrimaryService,
            Status:   "active",
            After:    s.session.StartedAt,
        })
        for _, alert := range alerts {
            s.sendNotification(ctx, "production_alert", map[string]any{
                "message": fmt.Sprintf("Alert: %s for service %s", alert.Summary, s.session.PrimaryService),
                "alert_id": alert.ID,
                "suggested_tool": "triage_alerts",
            })
        }
    }

    // 2. Did error rate change for files the agent recently modified?
    for _, file := range s.session.FilesModified {
        entity, _ := s.codeEntityStore.GetByPath(ctx, file)
        if entity != nil && entity.ErrorCount30d > entity.PreviousErrorCount30d {
            s.sendNotification(ctx, "code_risk_change", map[string]any{
                "file": file,
                "message": fmt.Sprintf("Error count for %s increased since your last edit", file),
                "new_errors": entity.ErrorCount30d - entity.PreviousErrorCount30d,
            })
        }
    }

    // 3. Did a teammate's investigation find something relevant?
    if s.session.PrimaryService != "" {
        parallel := s.findParallelInvestigations(ctx, s.session)
        for _, p := range parallel {
            if p.HasNewFindings {
                s.sendNotification(ctx, "team_finding", map[string]any{
                    "message": fmt.Sprintf("A teammate found something relevant: %s", p.LatestFinding),
                    "session_id": p.SessionID,
                })
            }
        }
    }
}

func (s *MCPServer) sendNotification(ctx context.Context, notificationType string, data map[string]any) {
    s.mcpServer.SendNotification(ctx, mcp.Notification{
        Method: "notifications/message",
        Params: map[string]any{
            "level":   "info",
            "logger":  "opentrace",
            "type":    notificationType,
            "data":    data,
        },
    })
}
```

## MCP Resources (Subscribable Production State)

```go
// Register resources that agents can subscribe to for real-time updates
func (s *MCPServer) registerResources() {
    // Per-service production status
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://services/{service}/status",
        Name:        "Service Production Status",
        Description: "Real-time production metrics for a service. Updates on significant changes.",
        MimeType:    "application/json",
    })

    // Code risk dashboard
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://code/risk-summary",
        Name:        "Code Risk Summary",
        Description: "Top risky code paths across all services. Updates when risk scores change.",
        MimeType:    "application/json",
    })

    // Active investigations feed
    s.mcpServer.AddResource(mcp.Resource{
        URI:         "opentrace://investigations/active",
        Name:        "Active Investigations",
        Description: "Currently active investigation sessions and their findings.",
        MimeType:    "application/json",
    })
}
```
