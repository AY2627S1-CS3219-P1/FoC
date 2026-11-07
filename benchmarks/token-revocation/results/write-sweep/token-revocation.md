# Access-token revocation check and durable-write latency: Redis vs Postgres vs Postgres-via-HTTP

A revocation check (read) with concurrent **durable** refresh/logout writes injected at varying fractions, measured against Redis, direct Postgres, and Postgres behind one HTTP round trip. Reads and writes are reported in **separate** histograms and percentiles; a combined histogram would let the infrequent slow writes vanish behind the dominant read traffic.

Each row is averaged across `--reps`. Achieved write/hit fractions are the realized mix in the measured window (the RNG target may drift under load). Write p99 *reps* column shows the min–max across repetitions for one representative concurrency, so the within-group variability is visible alongside the mean.

## **Redis dictionary** (`SET revoked_access_tokens:<sid> … EX <ttl>` to write; `EXISTS` to check)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 34209 | 2.847 | 3.921 | 5.729 | 24.590 | 0 | 2.8 |
| 1000 | 34077 | 30.140 | 32.906 | 36.974 | 48.120 | 0 | 3.2 |
| 10000 | 34783 | 292.460 | 310.008 | 328.608 | 348.325 | 0 | 3.3 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 10000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 30388 | 2.764 | 5.729 | 9.132 | 27.723 | 0 | 2.5 |
| 1000 | 30374 | 32.906 | 40.334 | 50.920 | 82.402 | 0 | 2.8 |
| 10000 | 27159 | 358.775 | 427.306 | 811.155 | 862.744 | 0 | 2.6 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 31 | 7.444 | 9.132 | 12.598 | 21.282 | 0 | 0 |
| 1000 | 30 | 38.051 | 45.319 | 55.595 | 77.738 | 0 | 0 |
| 10000 | 26 | 369.851 | 439.752 | 836.198 | 862.744 | 0 | 0 |

*At c=1000, write p99 across reps ranged 54.0–57.2 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 12161 | 8.615 | 13.353 | 33.018 | 183.794 | 0 | 1.5 |
| 1000 | 14751 | 68.143 | 78.862 | 141.710 | 287.699 | 0 | 1.9 |
| 10000 | 14169 | 700.898 | 859.824 | 1192.203 | 1263.735 | 0 | 1.8 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 124 | 9.680 | 16.354 | 39.325 | 171.109 | 0 | 0 |
| 1000 | 152 | 68.143 | 81.159 | 133.236 | 287.699 | 0 | 0 |
| 10000 | 142 | 700.898 | 859.824 | 1192.203 | 1223.819 | 0 | 0 |

*At c=1000, write p99 across reps ranged 129.4–137.1 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 7456 | 9.978 | 16.858 | 45.319 | 287.373 | 0 | 1.0 |
| 1000 | 11779 | 76.565 | 91.345 | 122.863 | 264.276 | 0 | 1.6 |
| 10000 | 11803 | 787.529 | 886.370 | 939.553 | 966.099 | 0 | 1.7 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 822 | 9.978 | 16.858 | 46.678 | 287.373 | 0 | 0 |
| 1000 | 1307 | 76.565 | 91.345 | 119.789 | 264.276 | 0 | 0 |
| 10000 | 1316 | 787.529 | 886.370 | 939.553 | 966.099 | 0 | 0 |

*At c=1000, write p99 across reps ranged 102.5–137.1 ms (mean above).*

## **Postgres, direct** (`INSERT … expires_at` to write; `SELECT 1 … WHERE sid=$1 AND expires_at > now()` to check)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 31744 | 2.686 | 6.261 | 11.212 | 35.897 | 0 | 3.1 |
| 1000 | 30249 | 33.865 | 40.334 | 46.678 | 76.695 | 0 | 3.3 |
| 10000 | 32061 | 310.008 | 348.325 | 380.301 | 403.119 | 0 | 3.6 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 10000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 30992 | 2.764 | 6.250 | 11.529 | 35.897 | 0 | 3.1 |
| 1000 | 30451 | 32.906 | 40.334 | 51.007 | 79.130 | 0 | 3.4 |
| 10000 | 29314 | 348.916 | 403.119 | 439.752 | 466.137 | 0 | 3.3 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 32 | 9.414 | 17.335 | 30.345 | 39.993 | 0 | 0 |
| 1000 | 30 | 38.051 | 50.920 | 66.214 | 83.878 | 0 | 0 |
| 10000 | 28 | 348.916 | 403.119 | 452.945 | 466.137 | 0 | 0 |

*At c=1000, write p99 across reps ranged 64.3–68.1 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 31304 | 2.534 | 5.729 | 10.876 | 34.881 | 0 | 3.1 |
| 1000 | 28824 | 33.922 | 42.826 | 54.342 | 81.297 | 0 | 3.2 |
| 10000 | 26700 | 381.593 | 440.499 | 469.306 | 497.465 | 0 | 3.1 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 313 | 13.353 | 30.962 | 91.345 | 168.208 | 0 | 0 |
| 1000 | 298 | 44.036 | 60.647 | 88.610 | 178.906 | 0 | 0 |
| 10000 | 272 | 392.042 | 440.499 | 481.752 | 510.657 | 0 | 0 |

*At c=1000, write p99 across reps ranged 86.0–91.2 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 24963 | 2.128 | 5.099 | 9.414 | 35.237 | 0 | 2.8 |
| 1000 | 24216 | 34.881 | 45.319 | 55.595 | 145.344 | 0 | 3.1 |
| 10000 | 24000 | 369.224 | 466.929 | 558.951 | 573.774 | 0 | 3.0 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 2762 | 11.193 | 22.675 | 41.544 | 113.797 | 0 | 0 |
| 1000 | 2673 | 44.036 | 62.466 | 86.029 | 189.640 | 0 | 0 |
| 10000 | 2670 | 380.301 | 480.121 | 573.774 | 608.201 | 0 | 0 |

*At c=1000, write p99 across reps ranged 86.0–86.0 ms (mean above).*

## **Postgres via HTTP round trip** (Variant C: same Postgres write/read through one HTTP hop)

**reads only — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 18491 | 5.099 | 9.398 | 14.555 | 34.881 | 0 | 6.7 |
| 1000 | 17835 | 57.311 | 68.259 | 86.175 | 115.126 | 0 | 6.8 |
| 10000 | 16811 | 571.832 | 681.061 | 1184.153 | 1539.814 | 0 | 7.3 |

**reads only — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 1000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |
| 10000 | 0 | 0.000 | 0.000 | 0.000 | 0.000 | 0 | 0 |

**writes 0.1% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 17549 | 5.248 | 9.680 | 15.428 | 76.691 | 0 | 6.6 |
| 1000 | 17908 | 55.595 | 68.259 | 86.175 | 115.321 | 0 | 7.0 |
| 10000 | 17564 | 555.177 | 623.797 | 1257.333 | 1539.814 | 0 | 7.7 |

**writes 0.1% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 17 | 11.885 | 21.392 | 30.345 | 42.826 | 0 | 0 |
| 1000 | 18 | 62.466 | 78.862 | 91.190 | 105.894 | 0 | 0 |
| 10000 | 16 | 555.177 | 623.797 | 1186.163 | 1375.084 | 1 | 0 |

*At c=1000, write p99 across reps ranged 91.2–91.2 ms (mean above).*

**writes 1.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 18951 | 4.670 | 8.866 | 14.555 | 66.972 | 0 | 7.2 |
| 1000 | 18194 | 55.595 | 66.214 | 81.159 | 115.321 | 0 | 7.0 |
| 10000 | 17185 | 571.832 | 661.225 | 1297.249 | 1637.746 | 0 | 7.4 |

**writes 1.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 191 | 12.576 | 21.247 | 42.826 | 86.477 | 0 | 0 |
| 1000 | 190 | 66.214 | 86.029 | 192.258 | 300.440 | 0 | 0 |
| 10000 | 172 | 571.832 | 681.061 | 1257.333 | 1539.814 | 2 | 0 |

*At c=1000, write p99 across reps ranged 108.6–275.9 ms (mean above).*

**writes 10.0% — reads**

| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 16030 | 4.157 | 7.890 | 12.954 | 53.097 | 0 | 6.8 |
| 1000 | 16606 | 53.976 | 64.286 | 76.565 | 109.348 | 0 | 7.2 |
| 10000 | 14970 | 588.488 | 724.377 | 1255.202 | 1632.203 | 0 | 7.3 |

**writes 10.0% — writes (durable ack)**

| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 100 | 1773 | 13.731 | 26.154 | 46.678 | 163.357 | 0 | 0 |
| 1000 | 1837 | 64.286 | 76.565 | 91.190 | 125.694 | 0 | 0 |
| 10000 | 1666 | 588.488 | 744.214 | 1255.202 | 1539.814 | 64 | 0 |

*At c=1000, write p99 across reps ranged 91.2–91.2 ms (mean above).*

