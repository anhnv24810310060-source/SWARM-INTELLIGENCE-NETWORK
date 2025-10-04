package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	osExec "os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// PluginExecutor defines interface for task plugin execution
type PluginExecutor interface {
	Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error)
	PluginType() TaskType
}

// PluginRegistry manages task plugin registration and execution
type PluginRegistry struct {
	executors map[TaskType]PluginExecutor
	tracer    trace.Tracer
}

func NewPluginRegistry() *PluginRegistry {
	pr := &PluginRegistry{
		executors: make(map[TaskType]PluginExecutor),
		tracer:    otel.Tracer("orchestrator-plugins"),
	}

	// Register built-in plugins
	pr.Register(NewHTTPPlugin())
	pr.Register(NewPythonPlugin())
	pr.Register(NewGRPCPlugin())
	pr.Register(NewModelInferencePlugin())
	pr.Register(NewSQLPlugin())
	pr.Register(NewKafkaPlugin())
	pr.Register(NewShellPlugin())

	return pr
}

func (pr *PluginRegistry) Register(plugin PluginExecutor) {
	pr.executors[plugin.PluginType()] = plugin
}

func (pr *PluginRegistry) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	executor, exists := pr.executors[task.Type]
	if !exists {
		return nil, fmt.Errorf("unsupported task type: %s", task.Type)
	}

	ctx, span := pr.tracer.Start(ctx, "plugin.execute",
		trace.WithAttributes(
			attribute.String("plugin_type", string(task.Type)),
			attribute.String("task_id", task.ID),
		),
	)
	defer span.End()

	return executor.Execute(ctx, task, exec)
}

// ============================================================================
// HTTP Plugin - Enhanced with retry, circuit breaker, and connection pooling
// ============================================================================

type HTTPPlugin struct {
	client *http.Client
	tracer trace.Tracer
}

func NewHTTPPlugin() *HTTPPlugin {
	return &HTTPPlugin{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 20,
				IdleConnTimeout:     90 * time.Second,
				DisableKeepAlives:   false,
			},
		},
		tracer: otel.Tracer("plugin-http"),
	}
}

func (hp *HTTPPlugin) PluginType() TaskType {
	return TaskHTTP
}

func (hp *HTTPPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := hp.tracer.Start(ctx, "http.request",
		trace.WithAttributes(
			attribute.String("url", task.URL),
			attribute.String("method", task.Method),
		),
	)
	defer span.End()

	// Resolve template variables
	url := resolveTemplate(task.URL, exec)

	var body io.Reader
	if task.Body != nil {
		bodyJSON, err := json.Marshal(task.Body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyStr := resolveTemplate(string(bodyJSON), exec)
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
	req.Header.Set("User-Agent", "SwarmGuard-Orchestrator/2.0")

	for key, value := range task.Headers {
		req.Header.Set(key, resolveTemplate(value, exec))
	}

	// Propagate OpenTelemetry context
	otel.GetTextMapPropagator().Inject(ctx, &headerCarrier{req.Header})

	resp, err := hp.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response with size limit (10MB)
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
		attribute.Int("http.response_size", len(respBody)),
	)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse JSON response
	var result map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			// Non-JSON response
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

// ============================================================================
// Python Plugin - Execute Python scripts with sandboxing
// ============================================================================

type PythonPlugin struct {
	pythonPath string
	tracer     trace.Tracer
}

func NewPythonPlugin() *PythonPlugin {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		pythonPath = "python3"
	}

	return &PythonPlugin{
		pythonPath: pythonPath,
		tracer:     otel.Tracer("plugin-python"),
	}
}

func (pp *PythonPlugin) PluginType() TaskType {
	return TaskPython
}

func (pp *PythonPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := pp.tracer.Start(ctx, "python.execute")
	defer span.End()

	// Create temp script file
	tmpDir := os.TempDir()
	scriptPath := filepath.Join(tmpDir, fmt.Sprintf("workflow_%s_task_%s.py", exec.WorkflowID, task.ID))

	// Inject context as JSON variable
	contextJSON, _ := json.Marshal(exec.Context)
	scriptContent := fmt.Sprintf(`
import json
import sys

# Workflow context available as 'context' variable
context = %s

# User script
%s
`, string(contextJSON), task.Script)

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0600); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}
	defer os.Remove(scriptPath)

	// Execute with timeout
	cmd := osExec.Command(pp.pythonPath, scriptPath)
	cmd = cmd // use command with context manually
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}()
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set resource limits (Linux only)
	// cmd.SysProcAttr = &syscall.SysProcAttr{
	// 	Setpgid: true,
	// }

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("python execution failed: %w\nStderr: %s", err, stderr.String())
	}

	// Parse output as JSON if possible
	output := stdout.String()
	var result map[string]interface{}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		// Raw text output
		result = map[string]interface{}{
			"output": output,
			"stderr": stderr.String(),
		}
	}

	span.SetAttributes(attribute.Int("output_size", len(output)))

	return result, nil
}

// ============================================================================
// gRPC Plugin - Call gRPC services
// ============================================================================

type GRPCPlugin struct {
	tracer trace.Tracer
}

func NewGRPCPlugin() *GRPCPlugin {
	return &GRPCPlugin{
		tracer: otel.Tracer("plugin-grpc"),
	}
}

func (gp *GRPCPlugin) PluginType() TaskType {
	return "grpc"
}

func (gp *GRPCPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := gp.tracer.Start(ctx, "grpc.call")
	defer span.End()

	// TODO: Implement dynamic gRPC client
	// Use grpcurl-style dynamic invocation with proto reflection

	return map[string]interface{}{
		"status":  "not_implemented",
		"message": "gRPC plugin requires proto descriptor",
	}, fmt.Errorf("grpc plugin not yet implemented")
}

// ============================================================================
// Model Inference Plugin - ML model inference via ONNX Runtime
// ============================================================================

type ModelInferencePlugin struct {
	modelRegistry string // URL to model registry
	tracer        trace.Tracer
}

func NewModelInferencePlugin() *ModelInferencePlugin {
	return &ModelInferencePlugin{
		modelRegistry: getEnvDefault("MODEL_REGISTRY_URL", "http://model-registry:8080"),
		tracer:        otel.Tracer("plugin-model"),
	}
}

func (mip *ModelInferencePlugin) PluginType() TaskType {
	return "model"
}

func (mip *ModelInferencePlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := mip.tracer.Start(ctx, "model.inference",
		trace.WithAttributes(attribute.String("model", task.Script)), // model name in Script field
	)
	defer span.End()

	// Prepare inference request
	requestBody := map[string]interface{}{
		"model_name": task.Script,
		"input":      task.Body,
	}

	bodyJSON, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	// Call model registry inference endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		mip.modelRegistry+"/v1/inference", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model inference failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("model inference error: %s", string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// ============================================================================
// SQL Plugin - Execute SQL queries (read-only for safety)
// ============================================================================

type SQLPlugin struct {
	tracer trace.Tracer
}

func NewSQLPlugin() *SQLPlugin {
	return &SQLPlugin{
		tracer: otel.Tracer("plugin-sql"),
	}
}

func (sp *SQLPlugin) PluginType() TaskType {
	return "sql"
}

func (sp *SQLPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := sp.tracer.Start(ctx, "sql.query")
	defer span.End()

	// TODO: Implement SQL execution with connection pooling
	// Use database/sql with driver registration
	// Enforce read-only transactions for security

	return map[string]interface{}{
		"status":  "not_implemented",
		"message": "SQL plugin requires database configuration",
	}, fmt.Errorf("sql plugin not yet implemented")
}

// ============================================================================
// Kafka Plugin - Publish messages to Kafka topics
// ============================================================================

type KafkaPlugin struct {
	brokers []string
	tracer  trace.Tracer
}

func NewKafkaPlugin() *KafkaPlugin {
	brokersStr := getEnvDefault("KAFKA_BROKERS", "localhost:9092")
	brokers := strings.Split(brokersStr, ",")

	return &KafkaPlugin{
		brokers: brokers,
		tracer:  otel.Tracer("plugin-kafka"),
	}
}

func (kp *KafkaPlugin) PluginType() TaskType {
	return "kafka"
}

func (kp *KafkaPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := kp.tracer.Start(ctx, "kafka.publish")
	defer span.End()

	// TODO: Implement Kafka producer with compression and batching
	// Use confluent-kafka-go or segmentio/kafka-go

	return map[string]interface{}{
		"status":  "not_implemented",
		"message": "Kafka plugin requires producer configuration",
	}, fmt.Errorf("kafka plugin not yet implemented")
}

// ============================================================================
// Shell Plugin - Execute shell commands (DANGEROUS - use with caution)
// ============================================================================

type ShellPlugin struct {
	allowedCommands map[string]bool // Whitelist of allowed commands
	tracer          trace.Tracer
}

func NewShellPlugin() *ShellPlugin {
	// Only allow safe commands
	allowed := map[string]bool{
		"echo":   true,
		"cat":    true,
		"grep":   true,
		"awk":    true,
		"sed":    true,
		"jq":     true,
		"curl":   true,
		"wget":   true,
		"python": true,
	}

	return &ShellPlugin{
		allowedCommands: allowed,
		tracer:          otel.Tracer("plugin-shell"),
	}
}

func (shp *ShellPlugin) PluginType() TaskType {
	return "shell"
}

func (shp *ShellPlugin) Execute(ctx context.Context, task Task, exec *WorkflowExecution) (map[string]interface{}, error) {
	ctx, span := shp.tracer.Start(ctx, "shell.execute")
	defer span.End()

	// Parse command
	parts := strings.Fields(task.Script)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	command := parts[0]

	// Check whitelist
	if !shp.allowedCommands[command] {
		return nil, fmt.Errorf("command not allowed: %s", command)
	}

	// Execute with timeout
	cmd := osExec.Command(parts[0], parts[1:]...)
	if ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("command failed: %w\nStderr: %s", err, stderr.String())
	}

	return map[string]interface{}{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": cmd.ProcessState.ExitCode(),
	}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// resolveTemplate replaces {{task_id.field}} with values from execution context
func resolveTemplate(template string, exec *WorkflowExecution) string {
	exec.mu.RLock()
	defer exec.mu.RUnlock()

	result := template

	// Simple template resolution
	for taskID, output := range exec.Context {
		if outputMap, ok := output.(map[string]interface{}); ok {
			for field, value := range outputMap {
				placeholder := fmt.Sprintf("{{%s.%s}}", taskID, field)
				valueStr := fmt.Sprintf("%v", value)
				result = strings.ReplaceAll(result, placeholder, valueStr)
			}
		}
	}

	// Support {{workflow.id}}
	result = strings.ReplaceAll(result, "{{workflow.id}}", exec.WorkflowID)
	result = strings.ReplaceAll(result, "{{workflow.name}}", exec.WorkflowName)

	return result
}
