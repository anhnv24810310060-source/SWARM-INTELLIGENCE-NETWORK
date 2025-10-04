package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
	"github.com/open-policy-agent/opa/topdown"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// OPAEngine wraps official OPA SDK for production-grade policy evaluation
type OPAEngine struct {
	mu              sync.RWMutex
	preparedQueries map[string]*rego.PreparedEvalQuery // package name -> prepared query
	modules         map[string]*ast.Module             // file path -> parsed module
	policyDir       string
	defaultPackage  string
	compileLatency  metric.Float64Histogram
	prepareLatency  metric.Float64Histogram
	tracer          trace.Tracer
}

// NewOPAEngine creates a production-ready OPA policy engine
func NewOPAEngine(policyDir string, meter metric.Meter, tracer trace.Tracer) *OPAEngine {
	compileLatency, _ := meter.Float64Histogram("swarm_policy_compile_latency_ms",
		metric.WithDescription("Time to compile OPA policies"))
	prepareLatency, _ := meter.Float64Histogram("swarm_policy_prepare_latency_ms",
		metric.WithDescription("Time to prepare OPA query"))

	return &OPAEngine{
		preparedQueries: make(map[string]*rego.PreparedEvalQuery),
		modules:         make(map[string]*ast.Module),
		policyDir:       policyDir,
		defaultPackage:  "swarm.allow", // Default decision path: data.swarm.allow
		compileLatency:  compileLatency,
		prepareLatency:  prepareLatency,
		tracer:          tracer,
	}
}

// LoadPolicies loads all .rego files from policy directory
func (oe *OPAEngine) LoadPolicies(ctx context.Context) error {
	ctx, span := oe.tracer.Start(ctx, "opa.load_policies")
	defer span.End()

	start := time.Now()

	// Discover all .rego files
	files, err := filepath.Glob(filepath.Join(oe.policyDir, "*.rego"))
	if err != nil {
		return fmt.Errorf("glob policies: %w", err)
	}

	if len(files) == 0 {
		span.SetAttributes(attribute.Int("policy_count", 0))
		return fmt.Errorf("no policy files found in %s", oe.policyDir)
	}

	// Parse all modules
	newModules := make(map[string]*ast.Module)
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read policy %s: %w", file, err)
		}

		module, err := ast.ParseModule(file, string(content))
		if err != nil {
			return fmt.Errorf("parse policy %s: %w", file, err)
		}

		newModules[file] = module
	}

	// Compile and prepare queries for each package
	compiler := ast.NewCompiler()
	compiler.Compile(newModules)

	if compiler.Failed() {
		var errMsgs []string
		for _, err := range compiler.Errors {
			errMsgs = append(errMsgs, err.Error())
		}
		return fmt.Errorf("compile failed: %v", errMsgs)
	}

	// Prepare queries for each unique package
	packages := make(map[string]bool)
	for _, module := range newModules {
		pkg := module.Package.Path.String()
		packages[pkg] = true
	}

	newQueries := make(map[string]*rego.PreparedEvalQuery)
	for pkg := range packages {
		// Prepare query for decision path: data.<package>.allow
		query := fmt.Sprintf("data.%s.allow", pkg)

		prepareStart := time.Now()
		prepared, err := rego.New(
			rego.Query(query),
			rego.Compiler(compiler),
			rego.Store(nil), // Using in-memory store
		).PrepareForEval(ctx)

		if err != nil {
			return fmt.Errorf("prepare query for %s: %w", pkg, err)
		}

		newQueries[pkg] = &prepared

		oe.prepareLatency.Record(ctx, float64(time.Since(prepareStart).Milliseconds()),
			metric.WithAttributes(attribute.String("package", pkg)))
	}

	// Atomic update
	oe.mu.Lock()
	oe.modules = newModules
	oe.preparedQueries = newQueries
	oe.mu.Unlock()

	oe.compileLatency.Record(ctx, float64(time.Since(start).Milliseconds()),
		metric.WithAttributes(attribute.Int("policy_count", len(files))))

	span.SetAttributes(
		attribute.Int("policy_count", len(files)),
		attribute.Int("package_count", len(packages)),
	)

	return nil
}

// Evaluate executes policy evaluation with the given input
func (oe *OPAEngine) Evaluate(ctx context.Context, packageName string, input map[string]interface{}) (bool, map[string]interface{}, error) {
	ctx, span := oe.tracer.Start(ctx, "opa.evaluate",
		trace.WithAttributes(attribute.String("package", packageName)))
	defer span.End()

	oe.mu.RLock()
	prepared, exists := oe.preparedQueries[packageName]
	oe.mu.RUnlock()

	if !exists {
		// Try default package
		oe.mu.RLock()
		prepared, exists = oe.preparedQueries[oe.defaultPackage]
		oe.mu.RUnlock()

		if !exists {
			return false, nil, fmt.Errorf("no policy found for package: %s", packageName)
		}
	}

	// Execute prepared query
	results, err := prepared.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, nil, fmt.Errorf("eval failed: %w", err)
	}

	// Extract decision
	if len(results) == 0 {
		return false, nil, fmt.Errorf("no results from policy evaluation")
	}

	// Check if result is boolean
	if len(results[0].Expressions) == 0 {
		return false, nil, fmt.Errorf("no expressions in result")
	}

	decision := false
	if allow, ok := results[0].Expressions[0].Value.(bool); ok {
		decision = allow
	}

	// Extract additional metadata
	metadata := make(map[string]interface{})
	for key, value := range results[0].Bindings {
		metadata[key] = value
	}

	span.SetAttributes(attribute.Bool("decision", decision))

	return decision, metadata, nil
}

// EvaluateWithTrace executes policy with full trace for debugging
func (oe *OPAEngine) EvaluateWithTrace(ctx context.Context, packageName string, input map[string]interface{}) (bool, map[string]interface{}, *topdown.BufferTracer, error) {
	ctx, span := oe.tracer.Start(ctx, "opa.evaluate_with_trace")
	defer span.End()

	oe.mu.RLock()
	prepared, exists := oe.preparedQueries[packageName]
	oe.mu.RUnlock()

	if !exists {
		return false, nil, nil, fmt.Errorf("no policy found for package: %s", packageName)
	}

	// Create buffer tracer
	tracer := topdown.NewBufferTracer()

	results, err := prepared.Eval(ctx, rego.EvalInput(input), rego.EvalTracer(tracer))
	if err != nil {
		return false, nil, nil, fmt.Errorf("eval failed: %w", err)
	}

	if len(results) == 0 {
		return false, nil, tracer, fmt.Errorf("no results")
	}

	decision := false
	if len(results[0].Expressions) > 0 {
		if allow, ok := results[0].Expressions[0].Value.(bool); ok {
			decision = allow
		}
	}

	metadata := make(map[string]interface{})
	for key, value := range results[0].Bindings {
		metadata[key] = value
	}

	return decision, metadata, tracer, nil
}

// ListPolicies returns list of loaded policies
func (oe *OPAEngine) ListPolicies() []PolicyInfo {
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	policies := make([]PolicyInfo, 0, len(oe.modules))
	for path, module := range oe.modules {
		info := PolicyInfo{
			Path:    path,
			Package: module.Package.Path.String(),
			Rules:   make([]string, 0),
		}

		// Extract rule names
		for _, rule := range module.Rules {
			info.Rules = append(info.Rules, rule.Head.Name.String())
		}

		policies = append(policies, info)
	}

	return policies
}

// PolicyInfo contains metadata about a loaded policy
type PolicyInfo struct {
	Path    string   `json:"path"`
	Package string   `json:"package"`
	Rules   []string `json:"rules"`
}

// IsReady checks if OPA engine has policies loaded
func (oe *OPAEngine) IsReady() bool {
	oe.mu.RLock()
	defer oe.mu.RUnlock()
	return len(oe.modules) > 0
}

// ValidatePolicy validates a policy without loading it
func (oe *OPAEngine) ValidatePolicy(ctx context.Context, policyContent string) error {
	_, span := oe.tracer.Start(ctx, "opa.validate_policy")
	defer span.End()

	// Parse module
	module, err := ast.ParseModule("validation.rego", policyContent)
	if err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}

	// Compile
	compiler := ast.NewCompiler()
	compiler.Compile(map[string]*ast.Module{"validation.rego": module})

	if compiler.Failed() {
		var errMsgs []string
		for _, err := range compiler.Errors {
			errMsgs = append(errMsgs, err.Error())
		}
		return fmt.Errorf("compile failed: %v", errMsgs)
	}

	return nil
}

// GetModuleAST returns the AST for a specific module (for inspection)
func (oe *OPAEngine) GetModuleAST(path string) (*ast.Module, bool) {
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	module, exists := oe.modules[path]
	return module, exists
}

// Stats returns statistics about the OPA engine
func (oe *OPAEngine) Stats() map[string]interface{} {
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	totalRules := 0
	for _, module := range oe.modules {
		totalRules += len(module.Rules)
	}

	return map[string]interface{}{
		"modules_loaded":    len(oe.modules),
		"packages_prepared": len(oe.preparedQueries),
		"total_rules":       totalRules,
		"default_package":   oe.defaultPackage,
		"ready":             len(oe.modules) > 0,
	}
}

// PartialEval performs partial evaluation for distributed policy enforcement
func (oe *OPAEngine) PartialEval(ctx context.Context, packageName string, input map[string]interface{}, unknowns []string) (interface{}, error) {
	ctx, span := oe.tracer.Start(ctx, "opa.partial_eval")
	defer span.End()

	oe.mu.RLock()
	compiler := ast.NewCompiler()
	for _, module := range oe.modules {
		compiler.Compile(map[string]*ast.Module{"": module})
	}
	oe.mu.RUnlock()

	query := fmt.Sprintf("data.%s.allow", packageName)

	// Perform partial evaluation using separate instance
	pq, err := rego.New(
		rego.Query(query),
		rego.Compiler(compiler),
		rego.Unknowns(unknowns),
	).PrepareForEval(ctx)

	if err != nil {
		return nil, fmt.Errorf("prepare partial eval: %w", err)
	}

	results, err := pq.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return nil, fmt.Errorf("partial eval failed: %w", err)
	}

	return results, nil
}

// BenchmarkPolicy measures performance of a specific policy
func (oe *OPAEngine) BenchmarkPolicy(ctx context.Context, packageName string, input map[string]interface{}, iterations int) (time.Duration, error) {
	start := time.Now()

	for i := 0; i < iterations; i++ {
		_, _, err := oe.Evaluate(ctx, packageName, input)
		if err != nil {
			return 0, err
		}
	}

	total := time.Since(start)
	return total / time.Duration(iterations), nil
}

// ExportBundle exports all loaded policies as an OPA bundle
func (oe *OPAEngine) ExportBundle() ([]byte, error) {
	oe.mu.RLock()
	defer oe.mu.RUnlock()

	bundle := map[string]interface{}{
		"modules": make([]map[string]interface{}, 0, len(oe.modules)),
	}

	for path, module := range oe.modules {
		bundle["modules"] = append(bundle["modules"].([]map[string]interface{}), map[string]interface{}{
			"path": path,
			"raw":  module.String(),
		})
	}

	return json.MarshalIndent(bundle, "", "  ")
}
