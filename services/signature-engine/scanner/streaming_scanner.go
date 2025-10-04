package scanner

import (
	"io"
	"sync"
)

// StreamingScanner allows scanning large payloads in chunks without loading entire data into memory.
// Thread-safe for concurrent use after construction.
type StreamingScanner struct {
	scanner    *AhoScanner
	bufferSize int
	overlapSize int // overlap between chunks to avoid missing patterns at boundaries
}

// NewStreamingScanner constructs a scanner with configurable buffer and overlap sizes.
// Recommended: bufferSize=64KB, overlapSize=max_pattern_length.
func NewStreamingScanner(scanner *AhoScanner, bufferSize, overlapSize int) *StreamingScanner {
	if bufferSize < 1024 {
		bufferSize = 65536 // 64KB default
	}
	if overlapSize < 0 {
		overlapSize = 4096 // 4KB default
	}
	return &StreamingScanner{
		scanner:     scanner,
		bufferSize:  bufferSize,
		overlapSize: overlapSize,
	}
}

// StreamMatch represents a match with global offset (across all chunks).
type StreamMatch struct {
	MatchResult
	GlobalOffset int64 // Absolute offset from stream start
}

// ScanStream scans an io.Reader and returns all matches with global offsets.
// Safe for concurrent use with different readers.
func (s *StreamingScanner) ScanStream(r io.Reader) ([]StreamMatch, error) {
	var results []StreamMatch
	buffer := make([]byte, s.bufferSize)
	overlap := make([]byte, 0, s.overlapSize)
	globalOffset := int64(0)

	for {
		// Read chunk with overlap prepended
		n := copy(buffer, overlap)
		nr, err := io.ReadAtLeast(r, buffer[n:], 1)
		if nr == 0 && err != nil {
			if err == io.EOF {
				break
			}
			return results, err
		}
		totalRead := n + nr

		// Scan current chunk
		chunk := buffer[:totalRead]
		matches := s.scanner.Scan(chunk)

		// Convert local offsets to global
		for _, m := range matches {
			sm := StreamMatch{
				MatchResult:  m,
				GlobalOffset: globalOffset + int64(m.Offset),
			}
			results = append(results, sm)
		}

		// Prepare overlap for next iteration (last N bytes of current chunk)
		overlapStart := totalRead - s.overlapSize
		if overlapStart < 0 {
			overlapStart = 0
		}
		overlap = overlap[:0]
		overlap = append(overlap, chunk[overlapStart:]...)

		// Update global offset (exclude overlap region from next iteration's offset calc)
		globalOffset += int64(totalRead - len(overlap))

		if err == io.EOF {
			break
		}
	}

	return results, nil
}

// WorkerPool provides concurrent scanning of multiple streams.
type WorkerPool struct {
	scanner *StreamingScanner
	workers int
	jobs    chan scanJob
	results chan scanResult
	wg      sync.WaitGroup
}

type scanJob struct {
	id     string
	reader io.Reader
}

type scanResult struct {
	id      string
	matches []StreamMatch
	err     error
}

// NewWorkerPool creates a pool of N workers for parallel scanning.
func NewWorkerPool(scanner *StreamingScanner, workers int) *WorkerPool {
	if workers < 1 {
		workers = 4
	}
	wp := &WorkerPool{
		scanner: scanner,
		workers: workers,
		jobs:    make(chan scanJob, workers*2),
		results: make(chan scanResult, workers*2),
	}
	wp.start()
	return wp
}

func (wp *WorkerPool) start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker()
	}
}

func (wp *WorkerPool) worker() {
	defer wp.wg.Done()
	for job := range wp.jobs {
		matches, err := wp.scanner.ScanStream(job.reader)
		wp.results <- scanResult{id: job.id, matches: matches, err: err}
	}
}

// Submit queues a scan job. Returns immediately.
func (wp *WorkerPool) Submit(id string, r io.Reader) {
	wp.jobs <- scanJob{id: id, reader: r}
}

// Results returns the result channel (read until closed).
func (wp *WorkerPool) Results() <-chan scanResult {
	return wp.results
}

// Close stops accepting new jobs and waits for all workers to finish.
func (wp *WorkerPool) Close() {
	close(wp.jobs)
	wp.wg.Wait()
	close(wp.results)
}
