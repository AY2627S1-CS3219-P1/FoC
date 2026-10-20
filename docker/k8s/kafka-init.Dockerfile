FROM apache/kafka:4.2.2@sha256:1213eb3943d551e5ed1fca7a4e109001cee35770b66a02a0c37a8964efe09b69
COPY infra/kafka /setup
COPY kafka /schemas
ENV KAFKA_SCHEMA_DIR=/schemas
ENTRYPOINT ["/bin/sh", "/setup/create-topics.sh"]
