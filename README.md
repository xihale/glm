# glm

Quota monitor and activation tool for the GLM coding plan and Google
Antigravity (Gemini), with systemd-based scheduling.

Both backends share the same 5-hour window model, so one daemon drives
either: it wakes at the reported reset (or the configured schedule), fires a
minimal request to anchor a fresh window, and verifies via the quota API.

## Providers

`glm` (default) — GLM coding plan quota via the bigmodel API.

`agy` — Google Antigravity / Gemini Code Assist (what the `agy` CLI uses).
Quota is reported per pool — `Gemini Models` and `Claude & GPT Models` — each
with a **5h bucket** plus a **weekly bucket**. Warmup is a single minimal
`generateContent` (maxOutputTokens 1, ~0.1% of the bucket). Weekly buckets
self-refill on a rolling 7-day window and cannot be accelerated by a request;
opt into `weekly: true` to have the daemon wake at the weekly reset moment and
fire a passive refresh anyway (costs ~0.02% of the weekly bucket; also anchors
the 5h window if that one happens to be fresh at the time).

Each pool can have its own schedule (and warmup model) via `agy.pools`; the
daemon then runs one independent anchor loop per pool. A pool with
`weekly_only: true` drops 5h anchoring entirely — its 5h window stays passive
(anchored by real usage whenever it happens) and the loop only keeps the
weekly bucket rolling: one warmup at startup while the bucket is fresh (a
window nobody ever requests stays "not started"), then again at each reported
weekly reset. With a single config
file, `--provider` picks the section (and systemd unit), so glm and agy
daemons run side by side:

| | glm | agy |
|---|---|---|
| config section | top level (`api_key`, `schedule`) | `agy:` (refresh_token, `pools`/`schedule`) |
| service | `glm.service` | `glm-agy.service` |

(An explicit `--config` path overrides the default file for all providers.)
Access token and project id are cached in `~/.cache/glm/agy.json` (0600) so a
warm `status` is a single HTTPS round trip (~1s) instead of three.

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

Every command takes the global `--provider agy` flag to operate on the agy
scope instead:

```bash
glm login --provider agy            # auto-import refresh token from the
                                    # local agy CLI login (or -k <token>)
glm status --provider agy           # both pools, 5h + weekly buckets
glm active --provider agy           # one-shot warmup of the 5h window
glm install --provider agy --auto   # glm-agy.service, independent schedule
glm reload --provider agy
glm uninstall --provider agy
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
schedule land in the right place. Install the agy daemon alongside with
`sudo glm install --provider agy --auto` (or pass times).

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

Single file: `~/.config/glm/config.yaml`, one section per provider, each with
its own schedule (the agy daemon falls back to the shared top-level schedule
when `agy.schedule` is absent).

```yaml
# --- glm provider (top level) ---
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

# --- agy provider ---
agy:
  refresh_token: 1//0xxxxx    # or omit to read it from the local agy CLI
  # client_id / client_secret: defaults to the Antigravity client
  # pool: gemini              # single-pool anchor (ignored when pools: is set)
  # model: gemini-2.5-flash   # warmup model (pool default if unset)
  # weekly: true              # passive refresh at the weekly reset moment
  # endpoint / project / token_file: rarely needed
  schedule:                   # agy's own policy; omit to share the top-level one
    timezone: "+8"
    times:
      - "06:00:00"
      - "16:00:00"
  pools:                      # multi-pool: one anchor loop per listed pool,
    gemini:                   # each with its own schedule/model/weekly
      schedule:
        timezone: "+8"
        times: ["06:00:00", "16:00:00"]
      weekly: true            # wake at the weekly reset, fire a warmup
    "3p":
      model: claude-sonnet-4-6
      weekly_only: true       # no 5h anchoring at all — the 5h window stays
                              # passive; the loop only keeps the weekly bucket
                              # rolling (startup fire while fresh, then at
                              # each weekly reset). schedule: is unused here.
```

Every `pools:` entry must set at least one key — an empty `pool: {}` is
dropped by viper's config parsing and the pool silently vanishes (give it
`weekly: false` or any explicit key instead).

`proxy` is shared; schedules are per provider (and per pool).

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
