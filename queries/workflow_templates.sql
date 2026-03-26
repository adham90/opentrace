-- Workflow templates: curated and learned tool sequences for investigation intents.
-- Table: workflow_templates (migration 000040)

-- name: GetWorkflowNextStep :many
SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
FROM workflow_templates
WHERE intent = sqlc.arg(intent) AND step_order = sqlc.arg(step_order)
ORDER BY resolved_session_count DESC;

-- name: GetWorkflowByName :many
SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
FROM workflow_templates
WHERE name = sqlc.arg(name)
ORDER BY step_order;

-- name: ListWorkflowTemplates :many
SELECT id, intent, name, step_order, tool_name, args_hint, source, resolved_session_count, created_at
FROM workflow_templates
WHERE intent = sqlc.arg(intent)
ORDER BY name, step_order;
