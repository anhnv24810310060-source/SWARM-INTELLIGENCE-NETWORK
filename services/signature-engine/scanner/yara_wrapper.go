package scanner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hillu/go-yara/v4"
)

// YaraEngine wraps YARA rule compiler and scanner
// Production-ready with hot-reload, namespace isolation, timeout protection
type YaraEngine struct {
	mu        sync.RWMutex
	rules     *yara.Rules
	namespace string
	version   string
	loadedAt  time.Time
}

// YaraMatch represents a YARA rule match result
type YaraMatch struct {
	RuleID    string            `json:"rule_id"`
	Namespace string            `json:"namespace"`
	Tags      []string          `json:"tags"`
	Meta      map[string]string `json:"meta"`
	Offset    int64             `json:"offset"`
	Length    int               `json:"length"`
	Severity  string            `json:"severity,omitempty"`
}

// NewYaraEngine compiles YARA rules from directory
func NewYaraEngine(rulesDir, namespace string) (*YaraEngine, error) {
	compiler, err := yara.NewCompiler()
	if err != nil {
		return nil, fmt.Errorf("yara compiler init: %w", err)
	}
	
	if namespace == "" {
		namespace = "default"
	}
	
	// Scan for .yar, .yara files
	ruleFiles := []string{}
	err = filepath.Walk(rulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (filepath.Ext(path) == ".yar" || filepath.Ext(path) == ".yara") {
			ruleFiles = append(ruleFiles, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk rules dir: %w", err)
	}
	
	if len(ruleFiles) == 0 {
		return nil, errors.New("no YARA rules found in " + rulesDir)
	}
	
	// Compile all rules into namespace
	for _, rfile := range ruleFiles {
		f, err := os.Open(rfile)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", rfile, err)
		}
		err = compiler.AddFile(f, namespace)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", rfile, err)
		}
	}
	
	rules, err := compiler.GetRules()
	if err != nil {
		return nil, fmt.Errorf("get rules: %w", err)
	}
	
	return &YaraEngine{
		rules:     rules,
		namespace: namespace,
		version:   fmt.Sprintf("%s-%d", namespace, time.Now().Unix()),
		loadedAt:  time.Now(),
	}, nil
}

// ScanBytes scans byte slice with timeout protection
func (ye *YaraEngine) ScanBytes(data []byte, timeoutSec int) ([]YaraMatch, error) {
	ye.mu.RLock()
	rules := ye.rules
	ye.mu.RUnlock()
	
	if rules == nil {
		return nil, errors.New("no rules loaded")
	}
	
	var matches []YaraMatch
	var scanErr error
	
	// Set scan timeout
	scanFlags := yara.ScanFlagsFastMode
	err := rules.ScanMemWithCallback(data, scanFlags, timeoutSec, func(m *yara.MatchRule) (bool, error) {
		// Extract metadata
		meta := make(map[string]string)
		for _, metaItem := range m.Metas {
			meta[metaItem.Identifier] = fmt.Sprintf("%v", metaItem.Value)
		}
		
		severity := meta["severity"]
		if severity == "" {
			severity = "medium"
		}
		
		// For each string match in the rule
		for _, str := range m.Strings {
			for _, match := range str.Matches {
				matches = append(matches, YaraMatch{
					RuleID:    m.Rule,
					Namespace: m.Namespace,
					Tags:      m.Tags,
					Meta:      meta,
					Offset:    int64(match.Offset),
					Length:    len(match.Data),
					Severity:  severity,
				})
			}
		}
		
		return true, nil // continue scanning
	})
	
	if err != nil {
		scanErr = fmt.Errorf("yara scan: %w", err)
	}
	
	return matches, scanErr
}

// ScanFile scans a file with timeout protection
func (ye *YaraEngine) ScanFile(filePath string, timeoutSec int) ([]YaraMatch, error) {
	ye.mu.RLock()
	rules := ye.rules
	ye.mu.RUnlock()
	
	if rules == nil {
		return nil, errors.New("no rules loaded")
	}
	
	var matches []YaraMatch
	scanFlags := yara.ScanFlagsFastMode
	
	err := rules.ScanFileWithCallback(filePath, scanFlags, timeoutSec, func(m *yara.MatchRule) (bool, error) {
		meta := make(map[string]string)
		for _, metaItem := range m.Metas {
			meta[metaItem.Identifier] = fmt.Sprintf("%v", metaItem.Value)
		}
		
		severity := meta["severity"]
		if severity == "" {
			severity = "medium"
		}
		
		for _, str := range m.Strings {
			for _, match := range str.Matches {
				matches = append(matches, YaraMatch{
					RuleID:    m.Rule,
					Namespace: m.Namespace,
					Tags:      m.Tags,
					Meta:      meta,
					Offset:    int64(match.Offset),
					Length:    len(match.Data),
					Severity:  severity,
				})
			}
		}
		
		return true, nil
	})
	
	if err != nil {
		return nil, fmt.Errorf("yara scan file: %w", err)
	}
	
	return matches, nil
}

// Reload recompiles rules from directory (hot-reload)
func (ye *YaraEngine) Reload(rulesDir string) error {
	newEngine, err := NewYaraEngine(rulesDir, ye.namespace)
	if err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}
	
	ye.mu.Lock()
	oldRules := ye.rules
	ye.rules = newEngine.rules
	ye.version = newEngine.version
	ye.loadedAt = newEngine.loadedAt
	ye.mu.Unlock()
	
	// Cleanup old rules
	if oldRules != nil {
		oldRules.Destroy()
	}
	
	return nil
}

// GetVersion returns current rule version
func (ye *YaraEngine) GetVersion() string {
	ye.mu.RLock()
	defer ye.mu.RUnlock()
	return ye.version
}

// Close releases YARA resources
func (ye *YaraEngine) Close() error {
	ye.mu.Lock()
	defer ye.mu.Unlock()
	
	if ye.rules != nil {
		ye.rules.Destroy()
		ye.rules = nil
	}
	
	return nil
}
