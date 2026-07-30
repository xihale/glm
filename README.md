# glm

GLM API quota monitor and activation tool, with systemd-based scheduling.

## Commands

```bash
glm login                 # set API key (prompts; or -k <key>)
glm status                # show current quota
glm active [-f]           # activate quota once (use --service to run as daemon)
glm install --auto        # install the scheduler as a systemd service
glm install +8 09:00 20:00 # install with a manual schedule (timezone + times)
glm reload                # reload config without restarting (SIGHUP)
glm uninstall             # stop, disable, and remove the service
```

`glm install` installs a **system service** by default (boot-persistent, no
login required). Use `--user` to install a user service instead. The daemon
runs `active --service`: activate, sleep until next run, repeat.

## Server deployment

On a headless server, install as a **system service** so it survives logout
and starts at boot — no lingering needed:

```bash
sudo glm install --auto
```

The service runs as the **invoking user** (resolved from `SUDO_USER`) and reads
that user's config, so first run `glm login` as yourself, then `sudo glm
install`. Under sudo, glm rewrites `HOME` to your real home so the config and
schedule land in the right place.

On a desktop or without root, use a **user service** instead (legacy behavior;
needs `loginctl enable-linger` to survive logout):

```bash
glm install --user --auto
```

Inspect and reload:

```bash
systemctl status glm            # system scope (or: systemctl --user status glm)
journalctl -u glm -f            # follow logs (or: journalctl --user -u glm)
glm reload                      # re-read config without restarting
glm uninstall                   # auto-detects scope and removes it
```

## Config

Default path: `~/.config/glm/config.yaml`

```yaml
api_key: sk-xxxxx
# base_url: https://...
# proxy: http://...

schedule:
  auto: true
  # or explicit:
  # timezone: +8
  # times:
  #   - "09:00:00"
  #   - "20:00:00"
```

Use a custom config path with `--config`:

```bash
glm --config ./config.yaml login
glm --config ./config.yaml install --auto
```

## Install

```bash
go install github.com/xihale/glm@latest
```

Or build from source:

```bash
go build -o glm .
```

## License

MIT
