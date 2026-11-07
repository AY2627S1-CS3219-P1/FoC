# Access-token revocation check and durable-write latency: Redis vs Postgres vs Postgres-via-HTTP

A revocation check (read) with concurrent **durable** refresh/logout writes injected at varying fractions, measured against Redis, direct Postgres, and Postgres behind one HTTP round trip. Reads and writes are reported in **separate** histograms and percentiles; a combined histogram would let the infrequent slow writes vanish behind the dominant read traffic.

Each row is averaged across `--reps`. Achieved write/hit fractions are the realized mix in the measured window (the RNG target may drift under load). Write p99 *reps* column shows the min–max across repetitions for one representative concurrency, so the within-group variability is visible alongside the mean.

## **Redis dictionary** (`SET revoked_access_tokens:<sid> … EX <ttl>` to write; `EXISTS` to check)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 25559 | 3.921 | 4.951 | 7.022 | 8.866 | 0 | 3.0 |
| 1000 | 26494 | 38.051 | 40.334 | 42.754 | 45.319 | 0 | 3.6 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 23545 | 3.921 | 6.250 | 11.193 | 17.840 | 0 | 2.5 |
| 1000 | 31114 | 33.865 | 38.051 | 42.754 | 48.038 | 0 | 3.0 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 19 | 8.866 | 14.131 | 15.877 | 15.877 | 0 | 0 |
| 1000 | 28 | 35.897 | 42.754 | 45.319 | 45.319 | 0 | 0 |

*At c=1000, write p99 across reps ranged 45.3–45.3 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 16692 | 7.022 | 8.866 | 13.331 | 16.830 | 0 | 1.6 |
| 1000 | 15096 | 68.143 | 76.565 | 86.029 | 91.190 | 0 | 2.2 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 178 | 7.444 | 8.866 | 14.131 | 15.877 | 0 | 0 |
| 1000 | 163 | 68.143 | 76.565 | 86.029 | 86.029 | 0 | 0 |

*At c=1000, write p99 across reps ranged 86.0–86.0 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 8842 | 8.866 | 14.131 | 16.830 | 22.522 | 0 | 1.4 |
| 1000 | 12435 | 72.231 | 76.565 | 81.159 | 91.190 | 0 | 2.0 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 1030 | 9.398 | 14.131 | 16.830 | 18.910 | 0 | 0 |
| 1000 | 1325 | 72.231 | 76.565 | 81.159 | 91.190 | 0 | 0 |

*At c=1000, write p99 across reps ranged 81.2–81.2 ms (mean above).*

## **Postgres, direct** (`INSERT … expires_at` to write; `SELECT 1 … WHERE sid=$1 AND expires_at > now()` to check)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 23270 | 4.157 | 6.250 | 9.962 | 23.873 | 0 | 3.4 |
| 1000 | 22324 | 45.319 | 50.920 | 53.976 | 60.647 | 0 | 3.3 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 29601 | 3.292 | 4.951 | 7.444 | 14.978 | 0 | 3.5 |
| 1000 | 34010 | 30.140 | 33.865 | 35.897 | 40.334 | 0 | 3.8 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 28 | 10.559 | 17.840 | 26.824 | 26.824 | 0 | 0 |
| 1000 | 30 | 31.948 | 40.334 | 40.334 | 40.334 | 0 | 0 |

*At c=1000, write p99 across reps ranged 40.3–40.3 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 36348 | 2.608 | 3.699 | 5.562 | 11.864 | 0 | 3.9 |
| 1000 | 35504 | 28.434 | 31.948 | 35.897 | 42.754 | 0 | 3.8 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 381 | 6.625 | 8.866 | 11.193 | 13.331 | 0 | 0 |
| 1000 | 346 | 31.948 | 38.051 | 45.319 | 45.319 | 0 | 0 |

*At c=1000, write p99 across reps ranged 45.3–45.3 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 23792 | 3.106 | 4.670 | 6.250 | 11.193 | 0 | 3.0 |
| 1000 | 20767 | 42.754 | 50.920 | 53.976 | 60.647 | 0 | 2.8 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 2685 | 8.364 | 10.559 | 14.131 | 15.877 | 0 | 0 |
| 1000 | 2293 | 48.038 | 57.214 | 60.647 | 68.143 | 0 | 0 |

*At c=1000, write p99 across reps ranged 60.6–60.6 ms (mean above).*

## **Postgres via HTTP round trip** (Variant C: same Postgres write/read through one HTTP hop)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 16479 | 5.896 | 9.398 | 13.331 | 21.247 | 0 | 6.2 |
| 1000 | 18524 | 53.976 | 60.647 | 68.143 | 81.159 | 0 | 6.9 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 20740 | 4.670 | 7.022 | 11.864 | 25.306 | 0 | 7.2 |
| 1000 | 20668 | 48.038 | 57.214 | 60.647 | 68.143 | 0 | 7.4 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 18 | 10.559 | 17.840 | 21.247 | 21.247 | 0 | 0 |
| 1000 | 20 | 53.976 | 68.143 | 81.159 | 81.159 | 0 | 0 |

*At c=1000, write p99 across reps ranged 81.2–81.2 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 19979 | 4.670 | 7.022 | 11.864 | 23.873 | 0 | 7.1 |
| 1000 | 15766 | 60.647 | 72.231 | 76.565 | 86.029 | 0 | 6.2 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 209 | 9.398 | 12.576 | 15.877 | 21.247 | 0 | 0 |
| 1000 | 168 | 68.143 | 76.565 | 86.029 | 91.190 | 0 | 0 |

*At c=1000, write p99 across reps ranged 86.0–86.0 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 12390 | 6.250 | 9.398 | 15.877 | 26.824 | 0 | 5.4 |
| 1000 | 14466 | 60.647 | 72.231 | 81.159 | 96.662 | 0 | 6.3 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 1444 | 12.576 | 17.840 | 23.873 | 33.865 | 0 | 0 |
| 1000 | 1547 | 68.143 | 81.159 | 91.190 | 102.462 | 0 | 0 |

*At c=1000, write p99 across reps ranged 91.2–91.2 ms (mean above).*

