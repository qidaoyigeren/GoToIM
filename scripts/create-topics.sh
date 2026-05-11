#!/bin/bash
# Wait for Kafka to be ready, then create required topics.
# Usage: ./create-topics.sh <kafka-host:port>

KAFKA=${1:-localhost:9092}
TOPICS=("goim-push-topic" "goim-room-topic" "goim-all-topic" "goim-ack-topic")

echo "Waiting for Kafka at $KAFKA..."
for i in $(seq 1 30); do
    if kafka-topics.sh --bootstrap-server "$KAFKA" --list > /dev/null 2>&1; then
        echo "Kafka is ready."
        break
    fi
    echo "  attempt $i/30..."
    sleep 2
done

for topic in "${TOPICS[@]}"; do
    echo "Creating topic: $topic"
    kafka-topics.sh --bootstrap-server "$KAFKA" \
        --create --if-not-exists \
        --topic "$topic" \
        --partitions 3 \
        --replication-factor 1
done

echo "All topics created."
kafka-topics.sh --bootstrap-server "$KAFKA" --list
