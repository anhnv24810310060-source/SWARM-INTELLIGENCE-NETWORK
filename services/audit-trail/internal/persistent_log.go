package internal
package internal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// PersistentAuditLog extends AppendLog with disk persistence
// Implements Write-Ahead Log (WAL) pattern for durability
type PersistentAuditLog struct {
	memory    *AppendLog
	mu        sync.RWMutex
	walFile   *os.File
	walPath   string
	redactor  *PIIRedactor
	compliance *ComplianceChecker
	segmentSize int64 // rotate segment after this size
}

type PersistentConfig struct {
	WALDir      string
	SegmentSize int64 // bytes
	EnablePII   bool
}

func NewPersistentAuditLog(cfg PersistentConfig) (*PersistentAuditLog, error) {
	if err := os.MkdirAll(cfg.WALDir, 0755); err != nil {
		return nil, fmt.Errorf("create wal dir: %w", err)
	}

	if cfg.SegmentSize == 0 {
		cfg.SegmentSize = 100 * 1024 * 1024 // 100MB default
	}

	wal := &PersistentAuditLog{
		memory:      NewAppendLog(),
		walPath:     cfg.WALDir,
		redactor:    NewPIIRedactor(cfg.EnablePII),
		compliance:  NewComplianceChecker(),
		segmentSize: cfg.SegmentSize,
	}

	// Open or create WAL segment
	if err := wal.openSegment(); err != nil {
		return nil, fmt.Errorf("open wal segment: %w", err)
	}

	// Restore from WAL on startup
	if err := wal.restoreFromWAL(); err != nil {
		return nil, fmt.Errorf("restore from wal: %w", err)
	}

	return wal, nil
}

// openSegment creates new WAL segment file
func (pal *PersistentAuditLog) openSegment() error {
	timestamp := time.Now().Unix()
	filename := filepath.Join(pal.walPath, fmt.Sprintf("audit-%d.log", timestamp))

	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open segment: %w", err)
	}

	pal.mu.Lock()
	if pal.walFile != nil {
		pal.walFile.Close()
	}
	pal.walFile = f
	pal.mu.Unlock()

	return nil
}

// rotateSegment creates new segment if current exceeds size limit
func (pal *PersistentAuditLog) rotateSegment() error {
	pal.mu.Lock()
	defer pal.mu.Unlock()

	if pal.walFile == nil {
		return nil
	}

	stat, err := pal.walFile.Stat()
	if err != nil {
		return err
	}

	if stat.Size() >= pal.segmentSize {
		pal.walFile.Close()
		return pal.openSegment()
	}

	return nil
}

// Append adds entry with PII redaction and WAL persistence
func (pal *PersistentAuditLog) Append(action, actor, resource, metadata string) (Entry, error) {
	// Apply PII redaction
	redactedMetadata := pal.redactor.Redact(metadata)
	redactedResource := pal.redactor.Redact(resource)

	// Create entry in memory
	entry := pal.memory.Append(action, actor, redactedResource, redactedMetadata)

	// Write to WAL
	pal.mu.Lock()
	if pal.walFile != nil {
		line, _ := json.Marshal(entry)
		line = append(line, '\n')
		if _, err := pal.walFile.Write(line); err != nil {
			pal.mu.Unlock()
			return Entry{}, fmt.Errorf("write wal: %w", err)
		}
		pal.walFile.Sync() // fsync for durability
	}
	pal.mu.Unlock()

	// Check if rotation needed
	pal.rotateSegment()

	return entry, nil
}

// AppendWithCompliance adds entry with compliance validation
func (pal *PersistentAuditLog) AppendWithCompliance(action, actor, resource, metadata, policyName string) (Entry, []string, error) {
	entry, err := pal.Append(action, actor, resource, metadata)
	if err != nil {
		return Entry{}, nil, err
	}

	// Validate compliance
	valid, violations := pal.compliance.Validate(entry, policyName)
	if !valid {
		// Log violation but don't reject entry
		fmt.Printf("Compliance violation [%s]: %v\n", policyName, violations)
	}

	return entry, violations, nil
}

// restoreFromWAL replays WAL files to rebuild in-memory state
func (pal *PersistentAuditLog) restoreFromWAL() error {
	files, err := filepath.Glob(filepath.Join(pal.walPath, "audit-*.log"))
	if err != nil {
		return fmt.Errorf("glob wal files: %w", err)
	}

	// Sort files by timestamp (encoded in filename)
	// Process in chronological order

	count := 0
	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("open wal file %s: %w", file, err)
		}

		decoder := json.NewDecoder(f)
		for {
			var entry Entry
			if err := decoder.Decode(&entry); err != nil {
				break // EOF or corrupted entry
			}

			// Restore to memory (bypass WAL write during restore)
			pal.memory.Append(entry.Action, entry.Actor, entry.Resource, entry.Metadata)
			count++
		}

		f.Close()
	}

	fmt.Printf("Restored %d entries from WAL\n", count)
	return nil
}

// Get retrieves entry by index
func (pal *PersistentAuditLog) Get(index uint64) (Entry, bool) {
	return pal.memory.Get(index)
}

// Latest returns most recent entry
func (pal *PersistentAuditLog) Latest() (Entry, bool) {
	return pal.memory.Latest()
}

// Verify checks integrity of entire log
func (pal *PersistentAuditLog) Verify() bool {
	return pal.memory.Verify()
}

// Query searches entries by filters (actor, action, time range)
func (pal *PersistentAuditLog) Query(filter QueryFilter) []Entry {
	pal.mu.RLock()
	defer pal.mu.RUnlock()

	results := []Entry{}
	for i := uint64(0); i < uint64(len(pal.memory.log)); i++ {
		entry := pal.memory.log[i]

		// Apply filters
		if filter.Actor != "" && entry.Actor != filter.Actor {
			continue
		}
		if filter.Action != "" && entry.Action != filter.Action {
			continue
		}
		if !filter.StartTime.IsZero() && entry.Timestamp.Before(filter.StartTime) {
			continue
		}
		if !filter.EndTime.IsZero() && entry.Timestamp.After(filter.EndTime) {
			continue
		}

		results = append(results, entry)

		// Limit results
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	return results
}

// QueryFilter defines search criteria
type QueryFilter struct {
	Actor     string
	Action    string
	Resource  string
	StartTime time.Time
	EndTime   time.Time
	Limit     int
}

// Close flushes and closes WAL
func (pal *PersistentAuditLog) Close() error {
	pal.mu.Lock()
	defer pal.mu.Unlock()

	if pal.walFile != nil {
		pal.walFile.Sync()
		return pal.walFile.Close()
	}

	return nil
}

// ExportToSIEM exports entries to external SIEM (placeholder for Kafka/HTTP)
func (pal *PersistentAuditLog) ExportToSIEM(entries []Entry, siemEndpoint string) error {
	// TODO: Implement Kafka producer or HTTP POST to SIEM
	// For now: log intent
	fmt.Printf("Exporting %d entries to SIEM: %s\n", len(entries), siemEndpoint)
	return nil
}
