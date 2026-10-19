FROM apache/kafka:4.2.2
COPY infra/kafka /setup
COPY kafka /schemas
ENV KAFKA_SCHEMA_DIR=/schemas
ENTRYPOINT ["/bin/sh", "/setup/create-topics.sh"]
