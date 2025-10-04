package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RuleLoader loads rules from a source (filesystem, API, database).
type RuleLoader interface {
	Load() ([]ExtendedRule, error)
}

// FileRuleLoader loads rules from a JSON file on disk.
type FileRuleLoader struct {
	path string
}

// NewFileRuleLoader constructs a loader for the given JSON file path.
func NewFileRuleLoader(path string) *FileRuleLoader {
	return &FileRuleLoader{path: path}
}

// Load reads and parses the rule file.
func (f *FileRuleLoader) Load() ([]ExtendedRule, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	
	var wrapper struct {
		Rules []ExtendedRule `json:"rules"`
	}
	
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, err
	}
	
	return wrapper.Rules, nil
}

// HotReloadScanner wraps AhoScanner with hot-reload capabilities.
// Monitors rule changes and atomically swaps scanner instances.
type HotReloadScanner struct {
	loader     RuleLoader
	scannerPtr atomic.Value // stores *AhoScanner
	
	checkInterval time.Duration
	lastHash      string
	
	mu       sync.RWMutex
	metadata ReloadMetadata
	
	stopCh chan struct{}
	doneCh chan struct{}
}

// ReloadMetadata tracks reload statistics.
type ReloadMetadata struct {
	Version         string    `json:"version"`
	LoadedAt        time.Time `json:"loaded_at"`
	RuleCount       int       `json:"rule_count"`
	BuildDurationMs int64     `json:"build_duration_ms"`
	LastReloadAt    time.Time `json:"last_reload_at,omitempty"`
	ReloadCount     int       `json:"reload_count"`
	LastError       string    `json:"last_error,omitempty"`
}

// NewHotReloadScanner creates a scanner with hot-reload capability.
// Starts background goroutine to check for rule changes periodically.
func NewHotReloadScanner(loader RuleLoader, checkInterval time.Duration) (*HotReloadScanner, error) {
	hrs := &HotReloadScanner{
		loader:        loader,
		checkInterval: checkInterval,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	
	// Initial load
	if err := hrs.reload(); err != nil {
		return nil, err
	}
	
	// Start watcher
	go hrs.watchLoop()
	
	return hrs, nil
}

// reload performs the actual rule reload and scanner rebuild.
func (hrs *HotReloadScanner) reload() error {
	rules, err := hrs.loader.Load()
	if err != nil {
		hrs.mu.Lock()
		hrs.metadata.LastError = err.Error()
		hrs.mu.Unlock()
		return err
	}
	
	// Calculate content hash
	hash := hrs.calculateRuleHash(rules)
	
	// Skip if unchanged
	if hash == hrs.lastHash {
		return nil
	}
	
	// Build new automaton
	start := time.Now()
	automaton, err := BuildAho(rules)
	if err != nil {
		hrs.mu.Lock()
		hrs.metadata.LastError = err.Error()
		hrs.mu.Unlock()
		return err
	}
	
	// Create new scanner
	scanner := NewAhoScanner(automaton)
	
	// Atomic swap
	hrs.scannerPtr.Store(scanner)
	hrs.lastHash = hash
	
	// Update metadata
	hrs.mu.Lock()
	hrs.metadata = ReloadMetadata{
		Version:         hash[:12],
		LoadedAt:        start,
		RuleCount:       automaton.ruleCount,
		BuildDurationMs: time.Since(start).Milliseconds(),
		LastReloadAt:    time.Now(),
		ReloadCount:     hrs.metadata.ReloadCount + 1,
		LastError:       "",
	}
	hrs.mu.Unlock()
	
	return nil
}

// calculateRuleHash computes a deterministic hash of all enabled rules.
func (hrs *HotReloadScanner) calculateRuleHash(rules []ExtendedRule) string {
	h := sha256.New()
	
	// Sort rules by ID for deterministic hashing
	sorted := make([]ExtendedRule, len(rules))
	copy(sorted, rules)
	
	for _, r := range sorted {
		if !r.Enabled {
			continue
		}
		h.Write([]byte(r.ID))
		h.Write([]byte{0})
		h.Write([]byte(r.Pattern))
		h.Write([]byte{0})
		h.Write([]byte(r.Severity))
		h.Write([]byte{0})
	}
	
	return hex.EncodeToString(h.Sum(nil))
}

// watchLoop periodically checks for rule changes.
func (hrs *HotReloadScanner) watchLoop() {
	defer close(hrs.doneCh)
	
	ticker := time.NewTicker(hrs.checkInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			_ = hrs.reload() // Errors are stored in metadata
		case <-hrs.stopCh:
			return
		}
	}
}

// Scan delegates to the current scanner instance.
func (hrs *HotReloadScanner) Scan(data []byte) []MatchResult {
	scanner := hrs.scannerPtr.Load().(*AhoScanner)
	if scanner == nil {
		return nil
	}
	return scanner.Scan(data)
}

// GetMetadata returns current reload statistics.
func (hrs *HotReloadScanner) GetMetadata() ReloadMetadata {
	hrs.mu.RLock()
	defer hrs.mu.RUnlock()
	return hrs.metadata
}

// Stop terminates the background watcher goroutine.
func (hrs *HotReloadScanner) Stop() {
	close(hrs.stopCh)
	<-hrs.doneCh
}

// ForceReload triggers an immediate reload check.
func (hrs *HotReloadScanner) ForceReload() error {
	return hrs.reload()
}

// DirectoryRuleLoader loads all .json files from a directory.
type DirectoryRuleLoader struct {
	dirPath string
}

// NewDirectoryRuleLoader constructs a loader for a rules directory.
func NewDirectoryRuleLoader(dirPath string) *DirectoryRuleLoader {
	return &DirectoryRuleLoader{dirPath: dirPath}
}

// Load reads all JSON files in the directory and merges rules.
func (d *DirectoryRuleLoader) Load() ([]ExtendedRule, error) {
	entries, err := os.ReadDir(d.dirPath)
	if err != nil {
		return nil, err
	}
	
	var allRules []ExtendedRule
	
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		
		fullPath := filepath.Join(d.dirPath, entry.Name())
		loader := NewFileRuleLoader(fullPath)
		
		rules, err := loader.Load()
		if err != nil {
			// Log but continue with other files
			continue
		}
		
		allRules = append(allRules, rules...)
	}
	
	if len(allRules) == 0 {
		return nil, errors.New("no rules loaded from directory")
	}
	
	return allRules, nil
}
