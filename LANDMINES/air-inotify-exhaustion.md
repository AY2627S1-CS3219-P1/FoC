# Air can exhaust a shared Linux inotify quota

**Applies when:** multiple development stacks share a Linux host and Air exits
before watching files with `too many open files`.

**Verified:** during PR #212's fresh Compose verification, Order and Communication
could not start Air. A separate container with only five open descriptors and
`RLIMIT_NOFILE=65536/65536` still received `EMFILE` from
`inotify_init1(IN_CLOEXEC)`. The host's `fs.inotify.max_user_instances` was 128.
Raising container `nofile` limits did not resolve it. Containers sharing a host
UID compete for this inotify quota independently of process descriptor limits.

**Workaround:** stop unused development watchers, or use Air's supported polling
mode in a local Compose override. For Order and Communication, whose normal
entrypoint is Air, the command is:

```yaml
services:
  order-service:
    command: [air, -c, .air.toml, -build.poll, "true", -build.poll_interval, "1000"]
  communication-service:
    command: [air, -c, .air.toml, -build.poll, "true", -build.poll_interval, "1000"]
```

Recreate only the affected containers after adding the override. Preserve the
migration entrypoints when adapting this workaround to User or Supplier.
Do not raise host-wide limits or stop another project's containers without
authorization. Image builds can succeed while the runtime watcher still fails;
check the application health RPCs rather than container status alone.

**Tracking issue:** [#213 Support Air startup when shared Linux inotify quota is exhausted](https://github.com/AY2627S1-CS3219-P1/FoC/issues/213).

**Remove this entry when:** a documented, supported Compose configuration or
startup fallback handles quota exhaustion and live reload has been verified
under that configuration. Close #213 after verification.
