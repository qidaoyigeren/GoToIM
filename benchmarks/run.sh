#!/bin/bash
# GoIM Benchmark Runner
# Usage: ./run.sh [comet_tcp_host:port] [logic_http_host:port] [duration]
# Default: localhost:3101 localhost:3111 60s

set -e

COMET_TCP=${1:-localhost:3101}
LOGIC_HTTP=${2:-localhost:3111}
COMET_WS="${COMET_TCP%%:*}:3102"
DURATION=${3:-60s}
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
OUTPUT_DIR="./benchmarks/results/${TIMESTAMP}"

mkdir -p "$OUTPUT_DIR"

echo "============================================"
echo "  GoIM Benchmark Suite"
echo "============================================"
echo "Comet TCP:  ${COMET_TCP}"
echo "Comet WS:   ${COMET_WS}"
echo "Logic HTTP: ${LOGIC_HTTP}"
echo "Duration:   ${DURATION}"
echo "Output:     ${OUTPUT_DIR}"
echo ""

# Phase 1: Connection benchmarks
for COUNT in 1000 5000 10000; do
    echo "=== Connection Benchmark: ${COUNT} connections ==="
    go run ./benchmarks/conn_bench \
        -host="${COMET_TCP}" \
        -count="${COUNT}" \
        -ramp=30s \
        -duration="${DURATION}" \
        -output="${OUTPUT_DIR}/conn_${COUNT}.json"
    echo ""
done

# Phase 2: Push benchmarks
for MSGS in 10000 50000 100000; do
    echo "=== Push Benchmark: ${MSGS} messages ==="
    go run ./benchmarks/push_bench \
        -logic-host="${LOGIC_HTTP}" \
        -comet-host="${COMET_WS}" \
        -receivers=100 \
        -rate=1000 \
        -duration="${DURATION}" \
        -output="${OUTPUT_DIR}/push_${MSGS}.json"
    echo ""
done

# Phase 3: Generate report
echo "=== Generating HTML Report ==="
go run ./benchmarks/report_gen \
    -conn="${OUTPUT_DIR}/conn_1000.json" \
    -push="${OUTPUT_DIR}/push_10000.json" \
    -output="${OUTPUT_DIR}/report.html"

echo ""
echo "============================================"
echo "  Benchmark Complete!"
echo "  Results: ${OUTPUT_DIR}/"
echo "  Report:  ${OUTPUT_DIR}/report.html"
echo "============================================"
