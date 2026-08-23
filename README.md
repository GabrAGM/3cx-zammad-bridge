> [!CAUTION]
> Support for v20 and above is experimental and may not work as expected. Please report any issues you encounter.
> Currently, only monitoring of extensions is supported. Monitoring of groups is not supported due to limitations of the 3CX permissions model.

# 3cx-zammad-bridge

Monitors calls in 3CX and communicates this to Zammad accordingly.

## Requirements

 - Linux x86_64

## Installation

- Download the latest release binary from [releases](https://github.com/qmexnetworks/3cx-zammad-bridge/releases).
    - Copy the binary into `/usr/local/bin`
- `chmod +x zammadbridge`

## Configuration

All configuration is done through the `config.yaml` file, that may appear in these locations:

- `/etc/3cx-zammad-bridge/config.yaml`
- `/opt/3cx-zammad-bridge/config.yaml`
- `config.yaml`  (within the working directory of this 3cx bridge process) 

The first (found) configuration file will be used. Also refer to the `config.yaml.dist` file.

For 3CX versions 20 and above, it's important that you create a client ID and secret in the 3CX web interface. 
You have to add all extensions that you want to monitor to the Call Control API permissions in the 3CX web interface for
the client ID you create.
Note that monitoring groups this way is not supported (due to limitations of the 3CX permissions model). 
You have to add all extensions manually.

Example configuration:

```yaml
Bridge:
  poll_interval: 0.5 # decimal; The number of seconds to wait in between polling 3CX for calls

3CX:
    # For versions below v20, define these two:
    user: "the username of a 3CX admin account"
    pass: "the password of a 3CX admin account"
    group: "the name of the 3CX group that should be monitored, for example Support"
    # For versions v20 and above, define these two:
    client_id: "the client ID you created in 'Admin' -> 'Integrations' -> 'API'"
    client_secret: "the secret that was shown once"
    # Always define these:
    host: "the URL of your 3CX server, including https://"
    extension_digits: 3 # numeric; How many digits the internal extensions have 
    trunk_digits: 5 # numeric; How many digits the numbers in the trunk have
    queue_extension: 816 # numeric; The number of the queue that the bridge should also listen to
    country_prefix: 49 # numeric; optional; The country dialing prefix to remove from the numbers

Zammad:
    endpoint: https://zammad.example.com/api/v1/cti/secret # The URL of your Zammad server, including the secret in the URL
    log_missed_queue_calls: true # boolean; Whether or not you want to log missed calls to your queue
```

## Auto-create tickets

The bridge can automatically create a Zammad ticket (and, if the caller is unknown, a Zammad user) whenever a call ends. Enable this with `auto_create_ticket: true` and set `api_url`, `api_token`, and `ticket_group` under `Zammad:`. Use `auto_create_directions` (`all` | `inbound` | `outbound` | `none`) and the `extension_filter_mode` / `extension_filter` keys to control which calls trigger creation.

**`extension_filter_mode` has no permissive default, on purpose.** If the key is
absent or empty the bridge auto-creates *nothing* and logs a warning at boot.
Set it explicitly:

| value | behaviour |
|---|---|
| *(absent/empty)* | fail closed — nothing is auto-created |
| `include` | only extensions listed in `extension_filter` create tickets |
| `exclude` | every extension **except** those listed creates tickets |
| `all` | every extension on the PBX creates tickets |

> **Upgrading from a build before this option existed:** a config that does not
> set `extension_filter_mode` will stop auto-creating tickets after the upgrade.
> That is deliberate — the previous implicit behaviour was `all`, which on a PBX
> shared between business lines files a ticket for every answered call company-wide.
> Add `extension_filter_mode: include` with the extensions that should open
> tickets (or `all` to keep the old behaviour) before rolling out.

### Repeat-call consolidation

`auto_create_dedup_window_minutes` (default `0`, off) consolidates repeat calls.
When a caller who already has a new/open ticket in `ticket_group` calls again
within the configured number of minutes, the bridge appends the call to that
ticket instead of creating a new one. Editable live in the admin UI. Lookups
fail open — if Zammad cannot be queried, a normal ticket is created.

## Running
 
Run the release binary to run the daemon. 

Example supervisord config:

```ini
[program:3cx-zammad-bridge]
command = /usr/local/bin/zammadbridge
autostart = true
autorestart = true
startretries = 10
stderr_logfile = /var/log/3cx-zammad-bridge.err.log
stdout_logfile = /var/log/3cx-zammad-bridge.out.log

# Optionally specify a user
user = zammad-bridge
```

Example systemd service:

```unit file (systemd)
[Unit]
Description=3cx-zammad-bridge
After=network.target

# One might want to wait for the 3CXGatewayService to be up and running
# before starting this service, but during updates the 3CGatewayService
# is *stopped* and later started. This results in this 3cx-zammad-bridge
# to be stopped but never started again.
#PartOf=3CXGatewayService.service

[Service]
User=zammad-bridge
Group=zammad-bridge
ExecStart=/usr/local/bin/zammadbridge
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Help
```
3cx-zammad-bridge is a bridge that listens on 3cx to forward information to zammad

Usage:
  zammadbridge [flags]

Flags:
  -c, --config string       custom config file path (default "/etc/3cx-zammad-bridge/config.yaml")
  -h, --help                help for zammadbridge
  -f, --log-format string   log format: "json" or "plain" (default "json")
      --trace               trace output, super verbose
  -v, --verbose             verbose output
```

## Development

You can build the binary by running `make build`

Theoretically, this should also run on Windows. You can compile it yourself and
report possible issues. 

## Support

Premium support is available at [Q-MEX Networks GmbH](https://www.qmex.net)
