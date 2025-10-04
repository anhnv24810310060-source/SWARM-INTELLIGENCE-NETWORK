package mainpackage
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// FederationNode represents a peer in the federation
type FederationNode struct {
	ID         string
	Address    string
	PublicKey  []byte
	LastSeen   time.Time
	Status     NodeStatus
	TrustScore float64 // 0.0 to 1.0
}

type NodeStatus string

const (
	StatusActive      NodeStatus = "active"
	StatusSuspicious  NodeStatus = "suspicious"
	StatusQuarantined NodeStatus = "quarantined"
	StatusOffline     NodeStatus = "offline"
)

// FederatedState holds replicated state across swarms
type FederatedState struct {
	mu sync.RWMutex
	
	// CRDT-based replicated data structures
	threatIntel    *LWWMap // threat intelligence shared across swarms
	detectionRules *ORSet  // detection rules (add/remove)
	nodeMetrics    *PNCounter // aggregated metrics
	
	// Membership tracking
	peers map[string]*FederationNode
	
	nodeID string
	
	// Sync state
	lastFullSync   time.Time
	peerVersions   map[string]VectorClock
	
	// Anti-entropy
	syncInterval   time.Duration
	syncBatchSize  int
}

// NewFederatedState creates new federated state
func NewFederatedState(nodeID string) *FederatedState {
	return &FederatedState{
		threatIntel:    NewLWWMap(nodeID),
		detectionRules: NewORSet(),
		nodeMetrics:    NewPNCounter(nodeID),
		peers:          make(map[string]*FederationNode),
		nodeID:         nodeID,
		peerVersions:   make(map[string]VectorClock),
		syncInterval:   30 * time.Second,
		syncBatchSize:  100,
	}
}

// AddPeer registers new peer node
func (fs *FederatedState) AddPeer(node *FederationNode) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	if node.ID == fs.nodeID {
		return fmt.Errorf("cannot add self as peer")
	}
	
	// Initialize trust score for new peer
	if node.TrustScore == 0 {
		node.TrustScore = 0.5 // neutral initial trust
	}
	
	node.LastSeen = time.Now()
	node.Status = StatusActive
	fs.peers[node.ID] = node
	
	// Initialize version vector for peer
	if fs.peerVersions[node.ID] == nil {
		fs.peerVersions[node.ID] = NewVectorClock()
	}
	
	slog.Info("peer added", "peer_id", node.ID, "address", node.Address)
	return nil
}

// RemovePeer removes peer from federation
func (fs *FederatedState) RemovePeer(nodeID string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	delete(fs.peers, nodeID)
	delete(fs.peerVersions, nodeID)
	
	slog.Info("peer removed", "peer_id", nodeID)
}

// UpdateThreatIntel adds/updates threat intelligence entry
func (fs *FederatedState) UpdateThreatIntel(key string, intel interface{}) {
	fs.threatIntel.Set(key, intel)
	slog.Debug("threat intel updated", "key", key)
}

// GetThreatIntel retrieves threat intelligence
func (fs *FederatedState) GetThreatIntel(key string) (interface{}, bool) {
	return fs.threatIntel.Get(key)
}

// AddDetectionRule adds detection rule to set
func (fs *FederatedState) AddDetectionRule(ruleID string) {
	tag := generateUniqueTag(fs.nodeID)
	fs.detectionRules.Add(ruleID, tag)
	slog.Debug("detection rule added", "rule_id", ruleID)
}

// RemoveDetectionRule removes detection rule
func (fs *FederatedState) RemoveDetectionRule(ruleID string) {
	fs.detectionRules.Remove(ruleID)
	slog.Debug("detection rule removed", "rule_id", ruleID)
}

// GetActiveRules returns all active detection rules
func (fs *FederatedState) GetActiveRules() []string {
	return fs.detectionRules.Items()
}

// IncrementMetric increments aggregated metric
func (fs *FederatedState) IncrementMetric(delta uint64) {
	fs.nodeMetrics.Increment(delta)
}

// GetMetricValue returns current metric value
func (fs *FederatedState) GetMetricValue() int64 {
	return fs.nodeMetrics.Value()
}

// SyncMessage represents state sync message
type SyncMessage struct {
	FromNode    string
	ToNode      string
	Timestamp   time.Time
	Version     VectorClock
	
	// Payload types
	Type        SyncType
	
	// Delta sync (efficient)
	ThreatDelta interface{}
	RulesDelta  interface{}
	MetricsDelta interface{}
	
	// Full state (fallback)
	FullState   []byte
}

type SyncType string

const (
	SyncTypeDelta    SyncType = "delta"
	SyncTypeFull     SyncType = "full"
	SyncTypeRequest  SyncType = "request"
	SyncTypeResponse SyncType = "response"
)

// StartAntiEntropy starts background anti-entropy process
func (fs *FederatedState) StartAntiEntropy(ctx context.Context) {
	ticker := time.NewTicker(fs.syncInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fs.runAntiEntropyRound()
		}
	}
}

// runAntiEntropyRound performs one anti-entropy sync round
func (fs *FederatedState) runAntiEntropyRound() {
	fs.mu.RLock()
	peers := make([]*FederationNode, 0, len(fs.peers))
	for _, peer := range fs.peers {
		if peer.Status == StatusActive {
			peers = append(peers, peer)
		}
	}
	fs.mu.RUnlock()
	
	if len(peers) == 0 {
		return
	}
	
	// Random peer selection for gossip-style sync
	selectedPeers := selectRandomPeers(peers, min(3, len(peers)))
	
	for _, peer := range selectedPeers {
		if err := fs.syncWithPeer(context.Background(), peer); err != nil {
			slog.Warn("sync failed", "peer_id", peer.ID, "error", err)
			fs.handleSyncFailure(peer)
		} else {
			fs.handleSyncSuccess(peer)
		}
	}
}

// syncWithPeer syncs state with specific peer
func (fs *FederatedState) syncWithPeer(ctx context.Context, peer *FederationNode) error {
	// Get peer's version
	fs.mu.RLock()
	peerVersion := fs.peerVersions[peer.ID].Copy()
	fs.mu.RUnlock()
	
	// Compute delta since peer's known version
	delta := fs.threatIntel.ComputeDelta(peerVersion)
	
	// Build sync message
	msg := SyncMessage{
		FromNode:    fs.nodeID,
		ToNode:      peer.ID,
		Timestamp:   time.Now(),
		Type:        SyncTypeDelta,
		ThreatDelta: delta,
	}
	
	// Send to peer (HTTP POST for simplicity, could use gRPC)
	return fs.sendSyncMessage(ctx, peer.Address, msg)
}

// sendSyncMessage sends sync message to peer
func (fs *FederatedState) sendSyncMessage(ctx context.Context, address string, msg SyncMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, 
		fmt.Sprintf("http://%s/federation/sync", address), 
		nil)
	if err != nil {
		return err
	}
	
	req.Header.Set("Content-Type", "application/json")
	
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync failed with status %d", resp.StatusCode)
	}
	
	return nil
}

// HandleSyncMessage processes incoming sync message
func (fs *FederatedState) HandleSyncMessage(msg SyncMessage) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// Verify peer is known
	peer, exists := fs.peers[msg.FromNode]
	if !exists {
		return fmt.Errorf("unknown peer: %s", msg.FromNode)
	}
	
	// Update last seen
	peer.LastSeen = time.Now()
	
	// Process based on message type
	switch msg.Type {
	case SyncTypeDelta:
		if delta, ok := msg.ThreatDelta.(*Delta); ok {
			fs.threatIntel.ApplyDelta(delta)
		}
		// Update peer version
		fs.peerVersions[msg.FromNode].Merge(msg.Version)
		
	case SyncTypeFull:
		// Full state sync (expensive, used as fallback)
		if err := fs.applyFullState(msg.FullState); err != nil {
			return err
		}
		
	case SyncTypeRequest:
		// Peer requesting our state
		return fs.respondToSyncRequest(msg.FromNode)
	}
	
	return nil
}

func (fs *FederatedState) applyFullState(data []byte) error {
	// Deserialize and merge full state
	remoteThreatIntel, err := DeserializeLWWMap(data, fs.nodeID)
	if err != nil {
		return err
	}
	
	fs.threatIntel.Merge(remoteThreatIntel)
	return nil
}

func (fs *FederatedState) respondToSyncRequest(peerID string) error {
	// Send current state to requesting peer
	peer, exists := fs.peers[peerID]
	if !exists {
		return fmt.Errorf("peer not found: %s", peerID)
	}
	
	data, err := SerializeLWWMap(fs.threatIntel)
	if err != nil {
		return err
	}
	
	msg := SyncMessage{
		FromNode:  fs.nodeID,
		ToNode:    peerID,
		Timestamp: time.Now(),
		Type:      SyncTypeResponse,
		FullState: data,
	}
	
	return fs.sendSyncMessage(context.Background(), peer.Address, msg)
}

// handleSyncSuccess updates peer trust score on successful sync
func (fs *FederatedState) handleSyncSuccess(peer *FederationNode) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// Increase trust score (exponential moving average)
	peer.TrustScore = 0.95*peer.TrustScore + 0.05*1.0
	if peer.TrustScore > 1.0 {
		peer.TrustScore = 1.0
	}
	
	peer.LastSeen = time.Now()
	peer.Status = StatusActive
}

// handleSyncFailure handles sync failure, decreases trust
func (fs *FederatedState) handleSyncFailure(peer *FederationNode) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	
	// Decrease trust score
	peer.TrustScore = 0.95*peer.TrustScore + 0.05*0.0
	
	// Update status based on trust
	if peer.TrustScore < 0.3 {
		peer.Status = StatusSuspicious
		slog.Warn("peer marked suspicious", "peer_id", peer.ID, "trust", peer.TrustScore)
	}
	
	if peer.TrustScore < 0.1 {
		peer.Status = StatusQuarantined
		slog.Error("peer quarantined", "peer_id", peer.ID, "trust", peer.TrustScore)
	}
	
	// Mark offline if not seen recently
	if time.Since(peer.LastSeen) > 5*time.Minute {
		peer.Status = StatusOffline
	}
}

// GetStats returns federation statistics
func (fs *FederatedState) GetStats() FederationStats {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	
	stats := FederationStats{
		NodeID:            fs.nodeID,
		TotalPeers:        len(fs.peers),
		ActivePeers:       0,
		SuspiciousPeers:   0,
		ThreatIntelCount:  len(fs.threatIntel.Keys()),
		DetectionRules:    fs.detectionRules.Size(),
		AggregatedMetrics: fs.nodeMetrics.Value(),
		LastFullSync:      fs.lastFullSync,
	}
	
	for _, peer := range fs.peers {
		switch peer.Status {
		case StatusActive:
			stats.ActivePeers++
		case StatusSuspicious, StatusQuarantined:
			stats.SuspiciousPeers++
		}
	}
	
	return stats
}

type FederationStats struct {
	NodeID            string
	TotalPeers        int
	ActivePeers       int
	SuspiciousPeers   int
	ThreatIntelCount  int
	DetectionRules    int
	AggregatedMetrics int64
	LastFullSync      time.Time
}

// Utility functions
func selectRandomPeers(peers []*FederationNode, count int) []*FederationNode {
	if count >= len(peers) {
		return peers
	}
	
	// Fisher-Yates shuffle and take first count
	selected := make([]*FederationNode, len(peers))
	copy(selected, peers)
	
	for i := len(selected) - 1; i > 0; i-- {
		j := randomInt(i + 1)
		selected[i], selected[j] = selected[j], selected[i]
	}
	
	return selected[:count]
}

func randomInt(max int) int {
	var b [4]byte
	rand.Read(b[:])
	return int(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24) % max
}

func generateUniqueTag(nodeID string) string {
	var b [8]byte
	rand.Read(b[:])
	return fmt.Sprintf("%s-%s-%d", nodeID, hex.EncodeToString(b[:]), time.Now().UnixNano())
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
