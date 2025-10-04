package internal
package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// GraphNode represents an entity in the threat graph (IP, domain, hash, etc.)
type GraphNode struct {
	ID         string
	Type       string // "ip", "domain", "hash", "user", "host"
	Value      string
	FirstSeen  time.Time
	LastSeen   time.Time
	Score      float64
	Attributes map[string]string
}

// GraphEdge represents a relationship between entities
type GraphEdge struct {
	From       string  // Node ID
	To         string  // Node ID
	Type       string  // "connects_to", "downloads", "resolves_to", "owned_by"
	Weight     float64 // relationship strength
	FirstSeen  time.Time
	LastSeen   time.Time
	EventCount int
}

// ThreatGraph maintains entity relationships for correlation
// In production: consider Neo4j or dgraph for scalability
type ThreatGraph struct {
	mu     sync.RWMutex
	nodes  map[string]*GraphNode
	edges  map[string]*GraphEdge // key: from:to:type
	index  map[string][]string   // node type -> node IDs for fast lookup
}

func NewThreatGraph() *ThreatGraph {
	return &ThreatGraph{
		nodes: make(map[string]*GraphNode),
		edges: make(map[string]*GraphEdge),
		index: make(map[string][]string),
	}
}

// AddNode adds or updates a node in the graph
func (tg *ThreatGraph) AddNode(node GraphNode) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	if node.ID == "" {
		node.ID = tg.generateNodeID(node.Type, node.Value)
	}

	existing, exists := tg.nodes[node.ID]
	if exists {
		// Update existing node
		existing.LastSeen = time.Now()
		existing.Score = (existing.Score + node.Score) / 2 // average scores
		for k, v := range node.Attributes {
			existing.Attributes[k] = v
		}
	} else {
		// New node
		node.FirstSeen = time.Now()
		node.LastSeen = time.Now()
		if node.Attributes == nil {
			node.Attributes = make(map[string]string)
		}
		tg.nodes[node.ID] = &node

		// Update index
		tg.index[node.Type] = append(tg.index[node.Type], node.ID)
	}
}

// AddEdge adds or updates an edge in the graph
func (tg *ThreatGraph) AddEdge(edge GraphEdge) {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	key := edgeKey(edge.From, edge.To, edge.Type)
	existing, exists := tg.edges[key]

	if exists {
		// Update existing edge
		existing.LastSeen = time.Now()
		existing.Weight = (existing.Weight + edge.Weight) / 2
		existing.EventCount++
	} else {
		// New edge
		edge.FirstSeen = time.Now()
		edge.LastSeen = time.Now()
		edge.EventCount = 1
		tg.edges[key] = &edge
	}
}

// FindRelated finds nodes related to given node within N hops
func (tg *ThreatGraph) FindRelated(nodeID string, maxHops int) []*GraphNode {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	visited := make(map[string]bool)
	result := make([]*GraphNode, 0)

	tg.bfs(nodeID, maxHops, visited, &result)

	return result
}

// bfs performs breadth-first search for related nodes
func (tg *ThreatGraph) bfs(startID string, maxHops int, visited map[string]bool, result *[]*GraphNode) {
	if maxHops == 0 {
		return
	}

	queue := []struct {
		id   string
		hops int
	}{{startID, 0}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current.id] {
			continue
		}
		visited[current.id] = true

		if node, exists := tg.nodes[current.id]; exists && current.id != startID {
			*result = append(*result, node)
		}

		if current.hops >= maxHops {
			continue
		}

		// Find connected nodes
		for edgeKey, edge := range tg.edges {
			if edge.From == current.id {
				queue = append(queue, struct {
					id   string
					hops int
				}{edge.To, current.hops + 1})
			}
			if edge.To == current.id {
				queue = append(queue, struct {
					id   string
					hops int
				}{edge.From, current.hops + 1})
			}
		}
	}
}

// FindAttackPath finds potential attack paths between two nodes
func (tg *ThreatGraph) FindAttackPath(fromID, toID string, maxDepth int) [][]*GraphNode {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	paths := make([][]*GraphNode, 0)
	currentPath := make([]*GraphNode, 0)
	visited := make(map[string]bool)

	tg.dfs(fromID, toID, maxDepth, visited, currentPath, &paths)

	return paths
}

// dfs performs depth-first search for attack paths
func (tg *ThreatGraph) dfs(currentID, targetID string, depth int, visited map[string]bool, path []*GraphNode, paths *[][]*GraphNode) {
	if depth == 0 {
		return
	}

	if visited[currentID] {
		return
	}

	node, exists := tg.nodes[currentID]
	if !exists {
		return
	}

	visited[currentID] = true
	path = append(path, node)

	if currentID == targetID {
		// Found path - make copy
		pathCopy := make([]*GraphNode, len(path))
		copy(pathCopy, path)
		*paths = append(*paths, pathCopy)
	} else {
		// Continue search
		for _, edge := range tg.edges {
			if edge.From == currentID {
				tg.dfs(edge.To, targetID, depth-1, visited, path, paths)
			}
		}
	}

	visited[currentID] = false
}

// CalculateThreatScore calculates aggregate threat score for a node
// considers: node's own score + connected nodes + temporal factors
func (tg *ThreatGraph) CalculateThreatScore(nodeID string) float64 {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	node, exists := tg.nodes[nodeID]
	if !exists {
		return 0.0
	}

	// Base score
	score := node.Score

	// Connected nodes influence
	connectedCount := 0
	connectedScoreSum := 0.0

	for _, edge := range tg.edges {
		if edge.From == nodeID || edge.To == nodeID {
			connectedCount++
			otherID := edge.To
			if edge.To == nodeID {
				otherID = edge.From
			}

			if otherNode, ok := tg.nodes[otherID]; ok {
				connectedScoreSum += otherNode.Score * edge.Weight
			}
		}
	}

	if connectedCount > 0 {
		// Add weighted influence from connected nodes (30% weight)
		connectedInfluence := (connectedScoreSum / float64(connectedCount)) * 0.3
		score += connectedInfluence
	}

	// Temporal factor: recent activity increases score
	timeSinceLastSeen := time.Since(node.LastSeen)
	if timeSinceLastSeen < 1*time.Hour {
		score *= 1.2 // 20% boost for very recent
	} else if timeSinceLastSeen < 24*time.Hour {
		score *= 1.1 // 10% boost for recent
	}

	// Cap at 10
	if score > 10 {
		score = 10
	}

	return score
}

// DetectAnomalousPatterns detects suspicious patterns in the graph
// Returns nodes that are part of anomalous patterns
func (tg *ThreatGraph) DetectAnomalousPatterns() []string {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	anomalous := make([]string, 0)

	// Pattern 1: High-degree nodes (potential C2 servers or pivots)
	for nodeID, node := range tg.nodes {
		degree := tg.getNodeDegree(nodeID)
		if degree > 50 && node.Score > 5.0 {
			anomalous = append(anomalous, nodeID)
		}
	}

	// Pattern 2: Dense subgraphs (potential coordinated attack)
	// Simplified: look for nodes with many interconnected neighbors

	// Pattern 3: Temporal anomalies (sudden burst of connections)
	recentThreshold := time.Now().Add(-5 * time.Minute)
	for nodeID := range tg.nodes {
		recentEdges := 0
		for _, edge := range tg.edges {
			if (edge.From == nodeID || edge.To == nodeID) && edge.LastSeen.After(recentThreshold) {
				recentEdges++
			}
		}
		if recentEdges > 20 {
			anomalous = append(anomalous, nodeID)
		}
	}

	return anomalous
}

// getNodeDegree counts edges connected to a node
func (tg *ThreatGraph) getNodeDegree(nodeID string) int {
	degree := 0
	for _, edge := range tg.edges {
		if edge.From == nodeID || edge.To == nodeID {
			degree++
		}
	}
	return degree
}

// Prune removes old nodes and edges to prevent memory bloat
func (tg *ThreatGraph) Prune(maxAge time.Duration) int {
	tg.mu.Lock()
	defer tg.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	pruned := 0

	// Remove old nodes
	for id, node := range tg.nodes {
		if node.LastSeen.Before(cutoff) {
			delete(tg.nodes, id)
			pruned++

			// Remove from index
			if ids, exists := tg.index[node.Type]; exists {
				newIDs := make([]string, 0, len(ids)-1)
				for _, nid := range ids {
					if nid != id {
						newIDs = append(newIDs, nid)
					}
				}
				tg.index[node.Type] = newIDs
			}
		}
	}

	// Remove old edges
	for key, edge := range tg.edges {
		if edge.LastSeen.Before(cutoff) {
			delete(tg.edges, key)
		}
	}

	return pruned
}

// GetStats returns graph statistics
func (tg *ThreatGraph) GetStats() map[string]interface{} {
	tg.mu.RLock()
	defer tg.mu.RUnlock()

	typeCount := make(map[string]int)
	for _, node := range tg.nodes {
		typeCount[node.Type]++
	}

	return map[string]interface{}{
		"total_nodes":  len(tg.nodes),
		"total_edges":  len(tg.edges),
		"nodes_by_type": typeCount,
	}
}

// Helper functions
func (tg *ThreatGraph) generateNodeID(nodeType, value string) string {
	h := sha256.Sum256([]byte(nodeType + ":" + value))
	return hex.EncodeToString(h[:16])
}

func edgeKey(from, to, edgeType string) string {
	return from + ":" + to + ":" + edgeType
}
