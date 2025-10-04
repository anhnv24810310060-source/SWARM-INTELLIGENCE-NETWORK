package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// TaskExecutor defines interface for executing different task types
type TaskExecutor interface {
	Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error)
}

// MultiTaskExecutor routes to appropriate executor based on task type
type MultiTaskExecutor struct {
	httpExecutor   *HTTPTaskExecutor
	scriptExecutor *ScriptTaskExecutor
	policyExecutor *PolicyTaskExecutor
}

func NewMultiTaskExecutor(httpClient *http.Client) *MultiTaskExecutor {
	return &MultiTaskExecutor{
		httpExecutor:   NewHTTPTaskExecutor(httpClient),
		scriptExecutor: NewScriptTaskExecutor(),
		policyExecutor: NewPolicyTaskExecutor(),
	}
}

func (mte *MultiTaskExecutor) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	switch task.Type {
	case TaskHTTP:
		return mte.httpExecutor.Execute(ctx, task, exec)
	case TaskPython:
		return mte.scriptExecutor.Execute(ctx, task, exec)
	case "policy":
		return mte.policyExecutor.Execute(ctx, task, exec)
	default:
		return nil, fmt.Errorf("unsupported task type: %s", task.Type)
	}
}

// HTTPTaskExecutor executes HTTP requests with connection pooling
type HTTPTaskExecutor struct {
	client *http.Client
	tracer trace.Tracer
}

func NewHTTPTaskExecutor(client *http.Client) *HTTPTaskExecutor {
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}
	
	return &HTTPTaskExecutor{
		client: client,
		tracer: otel.Tracer("orchestrator-http"),
	}
}

func (hte *HTTPTaskExecutor) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := hte.tracer.Start(ctx, "http.execute",
		trace.WithAttributes(
			attribute.String("url", task.URL),
			attribute.String("method", task.Method),
		),
	)
	defer span.End()
	
	// Resolve template variables in URL and body
	url := hte.resolveTemplate(task.URL, exec)
	
	var body io.Reader
	if task.Body != nil {
		bodyJSON, err := json.Marshal(task.Body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		
		// Replace template variables in body
		bodyStr := hte.resolveTemplate(string(bodyJSON), exec)
		body = strings.NewReader(bodyStr)
	}
	
	method := task.Method
	if method == "" {
		method = http.MethodPost
	}
	
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	
	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workflow-ID", exec.WorkflowID)
	req.Header.Set("X-Task-ID", task.ID)
	
	for key, value := range task.Headers {
		req.Header.Set(key, value)
	}
	
	// Propagate trace context
	otel.GetTextMapPropagator().Inject(ctx, &headerCarrier{req.Header})
	
	resp, err := hte.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	
	// Read response
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10MB limit
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	
	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	
	// Check status code
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http error %d: %s", resp.StatusCode, string(respBody))
	}
	
	// Parse response
	var result map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			// If not JSON, store as string
			result = map[string]interface{}{
				"body":        string(respBody),
				"status_code": resp.StatusCode,
			}
		}
	} else {
		result = map[string]interface{}{
			"status_code": resp.StatusCode,
		}
	}
	
	return result, nil
}

// resolveTemplate replaces {{task_id.field}} with actual values from execution context
func (hte *HTTPTaskExecutor) resolveTemplate(template string, exec *WorkflowExecution) string {
	exec.mu.RLock()
	defer exec.mu.RUnlock()
	
	result := template
	
	// Simple template resolution: {{task_id.field}}
	// In production, use a proper template engine
	for taskID, output := range exec.Context {
		if outputMap, ok := output.(map[string]interface{}); ok {
			for field, value := range outputMap {
				placeholder := fmt.Sprintf("{{%s.%s}}", taskID, field)
				valueStr := fmt.Sprintf("%v", value)
				result = strings.ReplaceAll(result, placeholder, valueStr)
			}
		}
	}
	
	return result
}

// ScriptTaskExecutor executes Python/shell scripts in sandboxed environment
type ScriptTaskExecutor struct {
	tracer trace.Tracer
}

func NewScriptTaskExecutor() *ScriptTaskExecutor {
	return &ScriptTaskExecutor{
		tracer: otel.Tracer("orchestrator-script"),
	}
}

func (ste *ScriptTaskExecutor) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := ste.tracer.Start(ctx, "script.execute",
		trace.WithAttributes(attribute.String("script", task.Script)),
	)
	defer span.End()
	
	// TODO: Implement sandbox execution using:
	// - gVisor for container isolation
	// - Resource limits (CPU, memory, time)
	// - Network isolation
	
	// For now, return mock result
	return map[string]interface{}{
		"status": "executed",
		"output": "script execution not implemented",
	}, nil
}

// PolicyTaskExecutor evaluates OPA policies
type PolicyTaskExecutor struct {
	tracer trace.Tracer
}

func NewPolicyTaskExecutor() *PolicyTaskExecutor {
	return &PolicyTaskExecutor{
		tracer: otel.Tracer("orchestrator-policy"),
	}
}

func (pte *PolicyTaskExecutor) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := pte.tracer.Start(ctx, "policy.execute",
		trace.WithAttributes(attribute.String("policy", task.Policy)),
	)
	defer span.End()
	
	// Call policy service
	policyURL := getEnvDefault("POLICY_SERVICE_URL", "http://policy-service:8080")
	
	requestBody := map[string]interface{}{
		"policy": task.Policy,
		"input":  exec.Context,
	}
	
	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, policyURL+"/v1/evaluate", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("policy service error: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("policy evaluation failed: %s", string(body))
	}
	
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	
	return result, nil
}

// headerCarrier adapts http.Header for OpenTelemetry propagation
type headerCarrier struct {
	header http.Header
}

func (hc *headerCarrier) Get(key string) string {
	return hc.header.Get(key)
}

func (hc *headerCarrier) Set(key, value string) {
	hc.header.Set(key, value)
}

func (hc *headerCarrier) Keys() []string {
	keys := make([]string, 0, len(hc.header))
	for k := range hc.header {
		keys = append(keys, k)
	}
	return keys
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
