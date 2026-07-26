---
title: Configuration
audience: technical
last_reviewed: 2026-07-26
---

# Configuration

All configuration is via `config.yaml`, resolved from the first path found
among:

- `/etc/3cx-zammad-bridge/config.yaml`
- `/opt/3cx-zammad-bridge/config.yaml`
- `config.yaml` (working directory of the process)

See `config.yaml.dist` in the repo for the shipped template.

## Reference

```yaml
Bridge:
  poll_interval: 0.5 # seconds between 3CX polls

3CX:
    # 3CX < v20:
    user: "3CX admin username"
    pass: "3CX admin password"
    group: "3CX group to monitor, e.g. Support"
    # 3CX >= v20 (Call Control API — create under Admin -> Integrations -> API,
    # then add every extension to monitor to that client's permissions;
    # group monitoring is not supported on v20+):
    client_id: "client ID"
    client_secret: "client secret"
    # Always required:
    host: "https://your-3cx-server"
    extension_digits: 3
    trunk_digits: 5
    queue_extension: 816
    country_prefix: 49 # optional

Zammad:
    endpoint: https://zammad.example.com/api/v1/cti/secret
    log_missed_queue_calls: true
```

## Running

The binary (`zammadbridge`) runs as a long-lived daemon — supervisord or
systemd, both documented in the repo
[README](https://github.com/AGM-One-Vision/3cx-zammad-bridge#running).

```
Usage:
  zammadbridge [flags]

Flags:
  -c, --config string       custom config file path (default "/etc/3cx-zammad-bridge/config.yaml")
  -h, --help                help for zammadbridge
  -f, --log-format string   log format: "json" or "plain" (default "json")
      --trace               trace output, super verbose
  -v, --verbose              verbose output
```

## Build

`make build` produces the release binary. Linux x86_64 is the only tested
target; Windows compiles but is unverified upstream.
