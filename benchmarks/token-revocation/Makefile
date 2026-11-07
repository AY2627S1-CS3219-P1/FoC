# Redis vs PostgreSQL: access-token revocation check latency as load scales.
#
# Standalone module (not in go.work, not linted by the repo). See README.md.

DOCKER_COMPOSE := docker compose -f bench-compose.yaml

# Full ladder — Redis, direct Postgres, and Postgres behind one HTTP round trip.
# Builds the image, starts postgres+redis, runs the benchmark, tears everything
# down (including the data volume) so each run starts clean. Raw results land in
# ./results/.
.PHONY: bench
bench:
	$(DOCKER_COMPOSE) build benchmark
	$(DOCKER_COMPOSE) run --rm benchmark
	$(DOCKER_COMPOSE) down -v

# Sweeps: custom concurrency/dataset/replication/pool/write settings. Examples:
#   make bench-tune LEVELS=100,1000,5000,10000 SESSIONS=1000000
#   make bench-tune LEVELS=1000 PG_POOLS=50,100,200,500 DURATION=20s REPS=3
#   make bench-tune WRITE_PERCENTAGES=0,0.1,1,10   # fraction of ops that are writes
# Bench-tune defaults to read-only (WRITE_PERCENTAGES=0); use bench-write to
# inject durable writes — see bench-write below.
.PHONY: bench-tune
bench-tune:
	$(DOCKER_COMPOSE) build benchmark
	$(DOCKER_COMPOSE) up -d postgres redis
	$(DOCKER_COMPOSE) run --rm benchmark \
		--levels "$(LEVELS-100,1000,10000)" \
		--sessions "$(SESSIONS-1000000)" \
		--write-percentages "$(WRITE_PERCENTAGES-0)" \
		--write-ids "$(WRITE_IDS-500000)" \
		--read-hit-rate "$(READ_HIT_RATE-0.01)" \
		--duration "$(DURATION-15s)" \
		--warmup "$(WARMUP-3s)" \
		--reps "$(REPS-2)" \
		--pg-pool "$(PG_POOL-100)" \
		--pg-pools "$(PG_POOLS)" \
		--out /out
	$(DOCKER_COMPOSE) down -v

# Durable-write sweep: the check-latency question (reads) with concurrent
# durable refresh/logout writes injected. Runs the full read-only baseline and
# each write fraction against all three variants, writing separate read/write
# latency tables. Example:
#   make bench-write LEVELS=100,1000,10000 WRITE_PERCENTAGES=0,0.1,1,10 REPS=3
# The compose stack pins durable Redis (appendfsync=always) and durable
# Postgres (fsync=on, synchronous_commit=on), so the durability gate passes.
.PHONY: bench-write
bench-write:
	$(DOCKER_COMPOSE) build benchmark
	$(DOCKER_COMPOSE) up -d postgres redis
	$(DOCKER_COMPOSE) run --rm benchmark \
		--levels "$(LEVELS-100,1000,10000)" \
		--sessions "$(SESSIONS-1000000)" \
		--write-percentages "$(WRITE_PERCENTAGES-0,0.1,1,10)" \
		--write-ids "$(WRITE_IDS-500000)" \
		--read-hit-rate "$(READ_HIT_RATE-0.01)" \
		--entry-ttl "$(ENTRY_TTL-24h)" \
		--duration "$(DURATION-15s)" \
		--warmup "$(WARMUP-3s)" \
		--reps "$(REPS-2)" \
		--pg-pool "$(PG_POOL-100)" \
		--pg-pools "$(PG_POOLS)" \
		--out /out
	$(DOCKER_COMPOSE) down -v

# Copy the latest run's outputs from the container to ./results for citation.
.PHONY: fetch
fetch:
	$(DOCKER_COMPOSE) cp benchmark:/out/. ./results/ 2>/dev/null || true
