package main

import (
	"context"
	"testing"
	"time"

	noopmetric "go.opentelemetry.io/otel/metric/noop"
)

func TestExecuteSimpleWorkflow(t *testing.T) {
	wf := Workflow{Name: "t", Tasks: []Task{{ID: "a", Type: TaskHTTP}, {ID: "b", Type: TaskHTTP, DependsOn: []string{"a"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mp := noopmetric.MeterProvider{}
	h, _ := mp.Meter("test").Float64Histogram("noop")
	if err := execute(ctx, wf, h); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
}

func TestExecuteDetectDeadlock(t *testing.T) {
	// cycle a->b, b->a should deadlock
	wf := Workflow{Name: "cycle", Tasks: []Task{
		{ID: "a", Type: TaskHTTP, DependsOn: []string{"b"}},
		{ID: "b", Type: TaskHTTP, DependsOn: []string{"a"}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mp := noopmetric.MeterProvider{}
	h, _ := mp.Meter("test").Float64Histogram("noop")
	if err := execute(ctx, wf, h); err == nil {
		t.Fatalf("expected deadlock error")
	}
}

func TestExecuteParallelFanOut(t *testing.T) {
	// a -> b,c,d ; ensure completes within reasonable time (parallel > sequential)
	wf := Workflow{Name: "fan", Tasks: []Task{
		{ID: "a", Type: TaskHTTP},
		{ID: "b", Type: TaskHTTP, DependsOn: []string{"a"}},
		{ID: "c", Type: TaskHTTP, DependsOn: []string{"a"}},
		{ID: "d", Type: TaskHTTP, DependsOn: []string{"a"}},
	}}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	mp := noopmetric.MeterProvider{}
	h, _ := mp.Meter("test").Float64Histogram("noop")
	if err := execute(ctx, wf, h); err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	dur := time.Since(start)
	if dur > 300*time.Millisecond { // heuristic threshold
		t.Fatalf("execution took too long: %v", dur)
	}
}
