#!/usr/bin/env bash
# Demo Script - PBFT Consensus with Advanced Features
# Usage: ./demo_consensus_features.sh

set -euo pipefail

echo "🚀 SWARM INTELLIGENCE - CONSENSUS DEMO"
echo "========================================"
echo ""

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
export CONSENSUS_DB_PATH="./data/demo-consensus"
export VALIDATOR_SET_SIZE=5
export CONSENSUS_CHECKPOINT_INTERVAL=10
export CONSENSUS_VIEW_CHANGE_ENABLED=true
export CONSENSUS_ROUND_TIMEOUT_MS=2000
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4317"

# Validator stakes (PoS weighted)
export CONSENSUS_VALIDATOR_STAKES="node-0=100,node-1=50,node-2=50,node-3=25,node-4=25"

echo -e "${BLUE}📋 Configuration:${NC}"
echo "  Validators: $VALIDATOR_SET_SIZE"
echo "  Checkpoint Interval: $CONSENSUS_CHECKPOINT_INTERVAL blocks"
echo "  View Change Timeout: ${CONSENSUS_ROUND_TIMEOUT_MS}ms"
echo "  Validator Stakes: $CONSENSUS_VALIDATOR_STAKES"
echo ""

# Clean previous data
rm -rf "$CONSENSUS_DB_PATH"
mkdir -p "$CONSENSUS_DB_PATH"

echo -e "${YELLOW}Building consensus-core service...${NC}"
cd services/consensus-core
cargo build --release 2>&1 | grep -E "Finished|error" || true
cd ../..

if [ ! -f "target/release/consensus-core" ]; then
    echo -e "${YELLOW}⚠️  Build failed, using debug binary${NC}"
    BINARY="target/debug/consensus-core"
else
    BINARY="target/release/consensus-core"
    echo -e "${GREEN}✓ Built successfully${NC}"
fi

echo ""
echo -e "${BLUE}Starting consensus service...${NC}"
$BINARY &
CONSENSUS_PID=$!
sleep 3

# Check health
echo -e "${YELLOW}Checking health endpoint...${NC}"
curl -s http://localhost:9090/status | jq '.' || echo "Health check failed"
echo ""

# Simulate consensus rounds
echo -e "${BLUE}📊 Simulating Consensus Rounds${NC}"
echo "================================"
echo ""

for i in {1..15}; do
    echo -e "${GREEN}Round $i:${NC}"
    
    # Propose block (via gRPC would be ideal, using HTTP proxy for demo)
    BLOCK_HASH=$(echo -n "block-$i" | sha256sum | cut -d' ' -f1)
    
    echo "  Proposing block: height=$i hash=$BLOCK_HASH"
    
    # Simulate votes from validators
    for v in {0..4}; do
        echo "    ✓ Vote from node-$v"
        sleep 0.1
    done
    
    # Check if checkpoint created
    if [ $((i % CONSENSUS_CHECKPOINT_INTERVAL)) -eq 0 ]; then
        echo -e "  ${YELLOW}📸 Checkpoint created at height $i${NC}"
    fi
    
    sleep 0.5
    echo ""
done

echo -e "${BLUE}📈 Fetching Metrics${NC}"
echo "==================="
curl -s http://localhost:9090/metrics | grep -E "swarm_(blockchain_height|consensus_round|consensus_byzantine|consensus_faults)" | head -10
echo ""

echo -e "${BLUE}🔍 Consensus State${NC}"
echo "=================="
curl -s http://localhost:9090/status | jq '{
    live, 
    ready, 
    uptime_ms, 
    config_version,
    byzantine_faults: .byzantine_faults_total,
    detections_total
}'
echo ""

echo -e "${YELLOW}Stopping service...${NC}"
kill $CONSENSUS_PID 2>/dev/null || true
wait $CONSENSUS_PID 2>/dev/null || true

echo ""
echo -e "${GREEN}✅ Demo completed!${NC}"
echo ""
echo "📂 Data persisted in: $CONSENSUS_DB_PATH"
echo "📊 Metrics available at: http://localhost:9090/metrics"
echo "📖 Check logs above for:"
echo "   - Checkpoint creation"
echo "   - View changes"
echo "   - Byzantine fault detection (if any)"
