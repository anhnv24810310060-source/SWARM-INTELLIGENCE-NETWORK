package internalpackage internal


import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// FeedCollector aggregates threat intelligence from external sources
// Supports: MITRE ATT&CK, AlienVault OTX, VirusTotal, custom feeds
type FeedCollector struct {
	client     *http.Client
	apiKeys    map[string]string // service -> API key
	store      IndicatorStore
	updateFreq time.Duration
}

type FeedConfig struct {
	VTAPIKey  string        // VirusTotal API key
	OTXAPIKey string        // AlienVault OTX API key
	UpdateInterval time.Duration // feed refresh interval
}

func NewFeedCollector(cfg FeedConfig, store IndicatorStore) *FeedCollector {
	return &FeedCollector{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		apiKeys: map[string]string{
			"virustotal": cfg.VTAPIKey,
			"otx":        cfg.OTXAPIKey,
		},
		store:      store,
		updateFreq: cfg.UpdateInterval,
	}
}

// Start begins background feed collection
func (fc *FeedCollector) Start(ctx context.Context) {
	ticker := time.NewTicker(fc.updateFreq)
	defer ticker.Stop()

	// Initial sync
	fc.syncAllFeeds(ctx)

	for {
		select {
		case <-ticker.C:
			fc.syncAllFeeds(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (fc *FeedCollector) syncAllFeeds(ctx context.Context) {
	// Parallel feed collection
	feeds := []func(context.Context) error{
		fc.syncMITREAttack,
		fc.syncAlienVaultOTX,
		fc.syncVirusTotal,
	}

	for _, fn := range feeds {
		go func(f func(context.Context) error) {
			if err := f(ctx); err != nil {
				// Log error but continue (partial failures acceptable)
				fmt.Printf("feed sync error: %v\n", err)
			}
		}(fn)
	}
}

// syncMITREAttack fetches MITRE ATT&CK indicators (techniques, tactics)
func (fc *FeedCollector) syncMITREAttack(ctx context.Context) error {
	// MITRE ATT&CK Enterprise dataset (public, no API key required)
	url := "https://raw.githubusercontent.com/mitre/cti/master/enterprise-attack/enterprise-attack.json"
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := fc.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch mitre: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mitre fetch failed: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Parse STIX bundle
	var stixBundle struct {
		Objects []struct {
			Type        string   `json:"type"`
			Name        string   `json:"name"`
			ExternalRefs []struct {
				ExternalID string `json:"external_id"`
			} `json:"external_references"`
			KillChainPhases []struct {
				PhaseName string `json:"phase_name"`
			} `json:"kill_chain_phases"`
		} `json:"objects"`
	}

	if err := json.Unmarshal(body, &stixBundle); err != nil {
		return fmt.Errorf("parse mitre json: %w", err)
	}

	// Extract attack techniques and store as indicators
	count := 0
	for _, obj := range stixBundle.Objects {
		if obj.Type == "attack-pattern" && len(obj.ExternalRefs) > 0 {
			techniqueID := obj.ExternalRefs[0].ExternalID
			
			// Calculate risk score based on kill chain phase
			score := 5.0 // default medium risk
			for _, phase := range obj.KillChainPhases {
				switch phase.PhaseName {
				case "execution", "exfiltration", "impact":
					score = 8.0 // high risk
				case "reconnaissance", "resource-development":
					score = 3.0 // low risk
				}
			}

			ind := Indicator{
				Type:      IndicatorTechnique,
				Value:     techniqueID,
				Source:    "mitre-attack",
				Score:     score,
				Metadata:  map[string]string{"name": obj.Name},
				ExpiresAt: time.Now().Add(30 * 24 * time.Hour), // 30 days TTL
			}
			
			if err := fc.store.Put(ind); err == nil {
				count++
			}
		}
	}

	fmt.Printf("MITRE ATT&CK: synced %d techniques\n", count)
	return nil
}

// syncAlienVaultOTX fetches recent threat indicators from OTX
func (fc *FeedCollector) syncAlienVaultOTX(ctx context.Context) error {
	apiKey := fc.apiKeys["otx"]
	if apiKey == "" {
		return fmt.Errorf("OTX API key not configured")
	}

	// Get subscribed pulses (last 24 hours)
	url := "https://otx.alienvault.com/api/v1/pulses/subscribed?limit=50"
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("X-OTX-API-KEY", apiKey)

	resp, err := fc.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch otx: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("otx fetch failed: %d", resp.StatusCode)
	}

	var otxResp struct {
		Results []struct {
			Name       string `json:"name"`
			Indicators []struct {
				Type  string `json:"type"`
				Value string `json:"indicator"`
			} `json:"indicators"`
			Tags []string `json:"tags"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&otxResp); err != nil {
		return fmt.Errorf("parse otx json: %w", err)
	}

	count := 0
	for _, pulse := range otxResp.Results {
		for _, ioc := range pulse.Indicators {
			indType := mapOTXType(ioc.Type)
			if indType == "" {
				continue // unsupported type
			}

			// Calculate score based on tags
			score := 6.0
			for _, tag := range pulse.Tags {
				switch tag {
				case "ransomware", "apt", "exploit":
					score = 9.0
				case "malware":
					score = 8.0
				}
			}

			ind := Indicator{
				Type:      indType,
				Value:     ioc.Value,
				Source:    "otx",
				Score:     score,
				Metadata:  map[string]string{"pulse": pulse.Name},
				ExpiresAt: time.Now().Add(7 * 24 * time.Hour), // 7 days TTL
			}

			if err := fc.store.Put(ind); err == nil {
				count++
			}
		}
	}

	fmt.Printf("AlienVault OTX: synced %d indicators\n", count)
	return nil
}

// syncVirusTotal fetches recent malicious file hashes
func (fc *FeedCollector) syncVirusTotal(ctx context.Context) error {
	apiKey := fc.apiKeys["virustotal"]
	if apiKey == "" {
		return fmt.Errorf("VirusTotal API key not configured")
	}

	// VT API v3: get recent files (requires premium API for feed access)
	// For demo: query specific known hashes or use hunting rules
	
	// Example: query for recent ransomware samples
	url := "https://www.virustotal.com/api/v3/intelligence/search?query=tag:ransomware&limit=50"
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("x-apikey", apiKey)

	resp, err := fc.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch vt: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		// API key invalid or insufficient quota
		return fmt.Errorf("VT auth failed: %d (check API key or quota)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vt fetch failed: %d", resp.StatusCode)
	}

	var vtResp struct {
		Data []struct {
			ID         string `json:"id"` // file hash
			Attributes struct {
				LastAnalysisStats struct {
					Malicious int `json:"malicious"`
					Suspicious int `json:"suspicious"`
				} `json:"last_analysis_stats"`
				Tags []string `json:"tags"`
			} `json:"attributes"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vtResp); err != nil {
		return fmt.Errorf("parse vt json: %w", err)
	}

	count := 0
	for _, file := range vtResp.Data {
		malCount := file.Attributes.LastAnalysisStats.Malicious
		suspCount := file.Attributes.LastAnalysisStats.Suspicious
		
		// Score based on detection ratio
		score := float64(malCount) / 10.0 // normalize to 0-10
		if score > 10 {
			score = 10
		}
		if score < 5 && suspCount > 5 {
			score = 6.0
		}

		ind := Indicator{
			Type:      IndicatorHash,
			Value:     file.ID, // SHA256 hash
			Source:    "virustotal",
			Score:     score,
			Metadata:  map[string]string{"detections": fmt.Sprintf("%d/%d", malCount, malCount+suspCount)},
			ExpiresAt: time.Now().Add(14 * 24 * time.Hour), // 14 days TTL
		}

		if err := fc.store.Put(ind); err == nil {
			count++
		}
	}

	fmt.Printf("VirusTotal: synced %d file hashes\n", count)
	return nil
}

// mapOTXType converts OTX indicator type to internal type
func mapOTXType(otxType string) string {
	switch otxType {
	case "IPv4", "IPv6":
		return IndicatorIP
	case "domain", "hostname":
		return IndicatorDomain
	case "FileHash-SHA256", "FileHash-MD5", "FileHash-SHA1":
		return IndicatorHash
	case "URL":
		return IndicatorURL
	default:
		return ""
	}
}
