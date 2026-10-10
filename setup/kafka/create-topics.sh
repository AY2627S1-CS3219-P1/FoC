#!/bin/sh
set -eu

while read -r topic partitions replicas; do
  # Skip blank lines and comments.
  case "$topic" in
  "" | \#*) continue ;;
  esac

  echo "Ensuring topic exists: $topic"

  /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "${KAFKA_BROKERS:-kafka:9092}" \
    --create \
    --if-not-exists \
    --topic "$topic" \
    --partitions "$partitions" \
    --replication-factor "$replicas"
done </setup/topics.conf
