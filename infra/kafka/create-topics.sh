#!/bin/sh
set -eu

setup_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
manifest=${TOPICS_CONFIG:-$setup_dir/topics.conf}
schema_root=${KAFKA_SCHEMA_DIR:-$setup_dir/../../kafka}
topics_cli=${KAFKA_TOPICS_CLI:-/opt/kafka/bin/kafka-topics.sh}

fail() {
  echo "$*" >&2
  exit 1
}

# Validate the entire manifest before creating any topics.
while read -r topic partitions replicas schema extra || [ -n "$topic" ]; do
  case "$topic" in "" | \#*) continue ;; esac
  [ -z "$extra" ] || fail "Unexpected fields for topic: $topic"
  for value in "$partitions" "$replicas"; do
    case "$value" in "" | *[!0-9]*) fail "Invalid topic settings: $topic" ;; esac
    [ "$value" -gt 0 ] || fail "Topic settings must be positive: $topic"
  done
  case "$schema" in
    "" | /* | *..* | *[!a-zA-Z0-9_./-]*) fail "Invalid schema path: $schema" ;;
    */events/v1/*.proto) ;;
    *) fail "Expected <service>/events/v1/<topic>.proto: $schema" ;;
  esac
  service=${schema%%/*}
  filename=${schema##*/}
  expected_topic=$service.${filename%.proto}
  [ "$topic" = "$expected_topic" ] || fail "Topic $topic must match schema topic $expected_topic"
  [ -f "$schema_root/$schema" ] || fail "Missing schema for $topic: $schema"
done <"$manifest"

while read -r topic partitions replicas schema extra || [ -n "$topic" ]; do
  case "$topic" in "" | \#*) continue ;; esac
  echo "Ensuring topic exists: $topic ($schema)"
  "$topics_cli" \
    --bootstrap-server "${KAFKA_BROKERS:-kafka:9092}" \
    --create --if-not-exists \
    --topic "$topic" --partitions "$partitions" --replication-factor "$replicas"
done <"$manifest"
