package internal
package internal

import (
	"testing"
	"time"
)

func TestThreatGraph_AddNode(t *testing.T) {
	tg := NewThreatGraph()

	node := GraphNode{
		Type:  "ip",
		Value: "192.168.1.100",
		Score: 7.0,
		Attributes: map[string]string{
			"country": "US",
			"asn":     "AS15169",
		},
	}

	tg.AddNode(node)

	// Verify node added
	stats := tg.GetStats()
	if total := stats["total_nodes"].(int); total != 1 {
		t.Errorf("expected 1 node, got %d", total)
	}

	// Add same node again - should update, not duplicate
	node.Score = 8.0
	tg.AddNode(node)

	stats = tg.GetStats()
	if total := stats["total_nodes"].(int); total != 1 {
		t.Errorf("expected 1 node after update, got %d", total)
	}
}

func TestThreatGraph_AddEdge(t *testing.T) {
	tg := NewThreatGraph()

	// Add nodes first
	node1 := GraphNode{Type: "ip", Value: "1.2.3.4", Score: 5.0}
	node2 := GraphNode{Type: "domain", Value: "evil.com", Score: 7.0}
	tg.AddNode(node1)
	tg.AddNode(node2)

	// Add edge
	edge := GraphEdge{
		From:   tg.generateNodeID("ip", "1.2.3.4"),
		To:     tg.generateNodeID("domain", "evil.com"),
		Type:   "resolves_to",
		Weight: 1.0,
	}

	tg.AddEdge(edge)

	stats := tg.GetStats()
	if totalEdges := stats["total_edges"].(int); totalEdges != 1 {
		t.Errorf("expected 1 edge, got %d", totalEdges)
	}

	// Add same edge again - should update event count
	tg.AddEdge(edge)

	// Verify edge updated (event count should be 2)
	tg.mu.RLock()
	key := edgeKey(edge.From, edge.To, edge.Type)
	if e, exists := tg.edges[key]; !exists {
		t.Error("edge not found")
	} else if e.EventCount != 2 {
		t.Errorf("expected event count 2, got %d", e.EventCount)
	}
	tg.mu.RUnlock()
}

func TestThreatGraph_FindRelated(t *testing.T) {
	tg := NewThreatGraph()

	// Build graph: IP -> Domain -> Hash
	nodeIP := GraphNode{Type: "ip", Value: "1.2.3.4", Score: 5.0}
	nodeDomain := GraphNode{Type: "domain", Value: "evil.com", Score: 7.0}
	nodeHash := GraphNode{Type: "hash", Value: "abc123", Score: 8.0}

	tg.AddNode(nodeIP)
	tg.AddNode(nodeDomain)
	tg.AddNode(nodeHash)

	ipID := tg.generateNodeID("ip", "1.2.3.4")
	domainID := tg.generateNodeID("domain", "evil.com")
	hashID := tg.generateNodeID("hash", "abc123")

	tg.AddEdge(GraphEdge{From: ipID, To: domainID, Type: "resolves_to", Weight: 1.0})
	tg.AddEdge(GraphEdge{From: domainID, To: hashID, Type: "serves", Weight: 1.0})

	// Find related within 1 hop from IP
	related := tg.FindRelated(ipID, 1)
	if len(related) != 1 {
		t.Errorf("expected 1 related node, got %d", len(related))
	}

	// Find related within 2 hops from IP
	related = tg.FindRelated(ipID, 2)
	if len(related) != 2 {
		t.Errorf("expected 2 related nodes, got %d", len(related))
	}
}

func TestThreatGraph_FindAttackPath(t *testing.T) {
	tg := NewThreatGraph()

	// Build attack chain: Initial Access (IP) -> Execution (Domain) -> Exfiltration (Hash)
	nodeIP := GraphNode{Type: "ip", Value: "attacker.ip", Score: 6.0}
	nodeDomain := GraphNode{Type: "domain", Value: "c2.evil.com", Score: 8.0}
	nodeHash := GraphNode{Type: "hash", Value: "malware_hash", Score: 9.0}

	tg.AddNode(nodeIP)
	tg.AddNode(nodeDomain)
	tg.AddNode(nodeHash)

	ipID := tg.generateNodeID("ip", "attacker.ip")
	domainID := tg.generateNodeID("domain", "c2.evil.com")
	hashID := tg.generateNodeID("hash", "malware_hash")

	tg.AddEdge(GraphEdge{From: ipID, To: domainID, Type: "connects_to", Weight: 1.0})
	tg.AddEdge(GraphEdge{From: domainID, To: hashID, Type: "delivers", Weight: 1.0})

	// Find attack path from IP to hash
	paths := tg.FindAttackPath(ipID, hashID, 5)

	if len(paths) == 0 {
		t.Fatal("expected at least 1 attack path")
	}

	// Verify path length (should be 3: IP -> Domain -> Hash)
	if len(paths[0]) != 3 {
		t.Errorf("expected path length 3, got %d", len(paths[0]))
	}
}

func TestThreatGraph_CalculateThreatScore(t *testing.T) {
	tg := NewThreatGraph()

	// Add node with connected high-risk entities
	node := GraphNode{Type: "ip", Value: "suspicious.ip", Score: 5.0}
	tg.AddNode(node)
	nodeID := tg.generateNodeID("ip", "suspicious.ip")

	// Add high-score connected nodes
	maliciousDomain := GraphNode{Type: "domain", Value: "known.malware.com", Score: 9.0}
	tg.AddNode(maliciousDomain)
	domainID := tg.generateNodeID("domain", "known.malware.com")

	tg.AddEdge(GraphEdge{From: nodeID, To: domainID, Type: "connects_to", Weight: 1.0})

	// Calculate threat score (should be influenced by connected node)
	score := tg.CalculateThreatScore(nodeID)

	if score <= 5.0 {
		t.Errorf("expected score > 5.0 due to connected high-risk node, got %.2f", score)
	}

	t.Logf("Calculated threat score: %.2f", score)
}

func TestThreatGraph_DetectAnomalousPatterns(t *testing.T) {
	tg := NewThreatGraph()

	// Create high-degree node (potential C2 server)
	c2Node := GraphNode{Type: "ip", Value: "c2.server", Score: 7.0}
	tg.AddNode(c2Node)
	c2ID := tg.generateNodeID("ip", "c2.server")

	// Add many connections (simulate C2 communication)
	for i := 0; i < 60; i++ {
		victimNode := GraphNode{Type: "ip", Value: string(rune(i)), Score: 2.0}
		tg.AddNode(victimNode)
		victimID := tg.generateNodeID("ip", string(rune(i)))

		tg.AddEdge(GraphEdge{
			From:   victimID,
			To:     c2ID,
			Type:   "connects_to",
			Weight: 1.0,
		})
	}

	anomalous := tg.DetectAnomalousPatterns()

	if len(anomalous) == 0 {
		t.Error("expected to detect high-degree node as anomalous")
	}

	// Verify C2 node detected
	found := false
	for _, id := range anomalous {
		if id == c2ID {
			found = true
			break
		}
	}

	if !found {
		t.Error("C2 node not detected in anomalous patterns")
	}
}

func TestThreatGraph_Prune(t *testing.T) {
	tg := NewThreatGraph()

	// Add old node
	oldNode := GraphNode{Type: "ip", Value: "old.ip", Score: 3.0}
	tg.AddNode(oldNode)

	// Manually set last seen to old time
	oldID := tg.generateNodeID("ip", "old.ip")
	tg.mu.Lock()
	tg.nodes[oldID].LastSeen = time.Now().Add(-48 * time.Hour)
	tg.mu.Unlock()

	// Add recent node
	recentNode := GraphNode{Type: "ip", Value: "recent.ip", Score: 5.0}
	tg.AddNode(recentNode)

	// Prune nodes older than 24 hours
	pruned := tg.Prune(24 * time.Hour)

	if pruned != 1 {
		t.Errorf("expected 1 pruned node, got %d", pruned)
	}

	// Verify old node removed
	stats := tg.GetStats()
	if total := stats["total_nodes"].(int); total != 1 {
		t.Errorf("expected 1 remaining node, got %d", total)
	}
}

func BenchmarkThreatGraph_AddNode(b *testing.B) {
	tg := NewThreatGraph()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		node := GraphNode{
			Type:  "ip",
			Value: string(rune(i % 1000)),
			Score: 5.0,
		}
		tg.AddNode(node)
	}
}

func BenchmarkThreatGraph_CalculateThreatScore(b *testing.B) {
	tg := NewThreatGraph()

	// Setup: create graph with 1000 nodes and 5000 edges
	for i := 0; i < 1000; i++ {
		node := GraphNode{
			Type:  "ip",
			Value: string(rune(i)),
			Score: float64(i % 10),
		}
		tg.AddNode(node)
	}

	for i := 0; i < 5000; i++ {
		fromID := tg.generateNodeID("ip", string(rune(i%1000)))
		toID := tg.generateNodeID("ip", string(rune((i+1)%1000)))

		tg.AddEdge(GraphEdge{
			From:   fromID,
			To:     toID,
			Type:   "connects_to",
			Weight: 1.0,
		})
	}

	// Benchmark score calculation
	testID := tg.generateNodeID("ip", string(rune(500)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tg.CalculateThreatScore(testID)
	}
}
