# Development Session Tracking

Extend session tracking beyond investigations. When an AI agent is writing code, refactoring, or reviewing, track that too and link it to production impact.

## Extended Session Types

```
Session intents (updated):
  investigation  → debugging a production issue (existing)
  development    → writing/modifying code (NEW)
  review         → reviewing a PR or code change (NEW)
  deployment     → deploying and monitoring a release (NEW)
  query          → simple data retrieval (existing)
  configuration  → setting up monitoring (existing)
  exploration    → browsing/exploring (existing)
```

## Development Session Detection

```go
func classifyDevelopmentIntent(context string, toolName string, args map[string]any) (string, string) {
    lower := strings.ToLower(context)

    // Development keywords
    devKeywords := []string{
        "adding", "implementing", "building", "creating feature",
        "refactoring", "modifying", "updating code", "writing",
        "fixing", "changing", "optimizing",
    }
    for _, kw := range devKeywords {
        if strings.Contains(lower, kw) {
            return "development", context
        }
    }

    // Review keywords
    reviewKeywords := []string{
        "reviewing", "review pr", "checking pr", "code review",
        "looking at changes", "pr #",
    }
    for _, kw := range reviewKeywords {
        if strings.Contains(lower, kw) {
            return "review", context
        }
    }

    // Deploy keywords
    deployKeywords := []string{
        "deploying", "deploy", "releasing", "shipping",
        "going to production", "pushing to prod",
    }
    for _, kw := range deployKeywords {
        if strings.Contains(lower, kw) {
            return "deployment", context
        }
    }

    return "", "" // not a development session
}
```

## Linking Development to Production

```
Development session (10:00 AM):
  Claude Code modifies orders_controller.rb
  → OpenTrace records: files_modified: ["orders_controller.rb"]

Deploy (11:00 AM):
  CI/CD sends deploy event → files: ["orders_controller.rb"]
  → OpenTrace links: deploy includes changes from dev session sess_100

Investigation (1:00 PM):
  Error rate spikes → new investigation starts
  → OpenTrace connects ALL the dots:
    deploy.files_changed includes "orders_controller.rb"
    dev session sess_100 modified this file 3 hours ago
    → investigation_context includes:
      "orders_controller.rb was modified 3 hours ago (session sess_100)
       and deployed at 11:00 AM. Error spike started at 12:45 PM."
```

## What Development Sessions Track

```sql
-- Additional columns on investigation_sessions for development sessions
ALTER TABLE investigation_sessions ADD COLUMN files_modified TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN files_read TEXT NOT NULL DEFAULT '[]';
ALTER TABLE investigation_sessions ADD COLUMN linked_deploy_id INTEGER DEFAULT NULL;
```
