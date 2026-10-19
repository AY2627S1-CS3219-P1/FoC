#!/usr/bin/env bash
# Opt-in local disruption test. Retain PVCs and verify committed state afterward.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
require kubectl
check_context
[[ $K8S_EXISTING = 0 && $K8S_CONTEXT = "kind-$K8S_CLUSTER" ]] || die 'Persistence smoke is restricted to the managed local kind cluster.'
note 'Restarting local PostgreSQL and Kafka pods without deleting PVCs. Applications may briefly become unready.'
bootstrap_query="SELECT count(*)::text || ':' || coalesce(min(user_id::text), '') FROM admin_bootstrap;"
# Get the operator-configured database name without displaying any credential.
user_database=$(k -n foc get secret user-db -o go-template='{{.data.DB_NAME | base64decode}}')
operator=$(k -n foc get secret postgres-admin -o go-template='{{.data.POSTGRES_USER | base64decode}}')
before=$(k -n foc exec statefulset/postgres -c postgres -- psql -X -U "$operator" -d "$user_database" -Atc "$bootstrap_query")
topic="foc.infrastructure-smoke-$(date +%s)-$$"
record="persistence-$RANDOM-$$"
kafka() { k -n foc exec statefulset/kafka -c kafka -- env KAFKA_HEAP_OPTS='-Xms32m -Xmx128m' "$@"; }
kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server 127.0.0.1:9092 --create --topic "$topic" --partitions 1 --replication-factor 1
printf '%s\n' "$record" | k -n foc exec -i statefulset/kafka -c kafka -- env KAFKA_HEAP_OPTS='-Xms32m -Xmx128m' \
    /opt/kafka/bin/kafka-console-producer.sh --bootstrap-server 127.0.0.1:9092 --topic "$topic" \
    --command-property acks=all
k -n foc rollout restart statefulset/postgres statefulset/kafka
k -n foc rollout status statefulset/postgres --timeout="$K8S_TIMEOUT"
k -n foc rollout status statefulset/kafka --timeout="$K8S_TIMEOUT"
after=$(k -n foc exec statefulset/postgres -c postgres -- psql -X -U "$operator" -d "$user_database" -Atc "$bootstrap_query")
[[ $after = "$before" ]] || die 'FAIL: PostgreSQL bootstrap state changed after pod replacement.'
received=$(kafka /opt/kafka/bin/kafka-console-consumer.sh --bootstrap-server 127.0.0.1:9092 \
    --topic "$topic" --from-beginning --max-messages 1 --timeout-ms 45000)
[[ $received = "$record" ]] || die 'FAIL: Kafka did not retain the acknowledged record.'
for app in "${K8S_APPS[@]}"; do k -n foc rollout status "deployment/$app" --timeout="$K8S_TIMEOUT"; done
note "PASS: PostgreSQL bootstrap identity and Kafka record survived pod replacement; PVCs retained. Test topic $topic retained."
