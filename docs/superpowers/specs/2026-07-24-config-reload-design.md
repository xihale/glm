# Config Reload for the Daemon

**Date:** 2026-07-24
**Status:** Design — pending implementation

## Problem

The daemon (`glm active --service`, launched by `glm install` as a systemd user
service) loads `config.yaml` exactly once at process start via
`config.InitConfig()` and never reads it again. Any change to
`~/.config/glm/config.yaml` made while the daemon is running — a new schedule,
a rotated API key, a changed proxy — is invisible to the running process. The
only way to apply it today is `systemctl --user restart glm.service`.

`glm.Client` also snapshots `config.Current.APIKey` / `BaseURL` into its own
fields at construction (`pkg/glm/client.go:51`), and the proxy is baked into
the `http.Transport` at client construction (`pkg/httputil/client.go:13`). So
"reloading config" must mean rebuilding the client, not just mutating fields.

## Goal

Let a running daemon pick up config changes without a full restart, and give
the user a convenient command to trigger it immediately.

## Design Decisions (confirmed with user)

1. **Reload scope: full re-read.** Re-read the entire `config.Current` and
   rebuild the client each activation loop, so `schedule`, `api_key`,
   `base_url`, and `proxy` all take effect without restart.
2. **Failure mode: keep old config.** If the reload fails (e.g. malformed YAML
   mid-edit), the daemon keeps the last good in-memory config and continues —
   it never exits due to a config error.
3. **Cross-process trigger via SIGHUP.** The daemon is a separate process from
   any `glm` invocation, so a `glm reload` subcommand cannot mutate the
   daemon's memory directly. It sends `SIGHUP` to the systemd-managed daemon,
   which the daemon interprets as "reload now and recompute the next run."
4. **Each loop re-reads disk; SIGHUP is for immediacy.** Because the daemon
   runs only a few times a day, re-reading the file at the top of every loop
   is essentially free and makes config changes self-applying on the next
   cycle. SIGHUP's added value is skipping the current sleep so a schedule
   change is reflected right away instead of after the existing wait.

## Design

### 1. `pkg/config/config.go` — new `Reload()` function

```go
// Reload re-reads the config file into Current. On any error it leaves
// Current unchanged so callers keep running with the last good config.
func Reload() error {
    if err := viper.ReadInConfig(); err != nil {
        return err
    }
    var fresh Config
    if err := viper.Unmarshal(&fresh); err != nil {
        return err
    }
    Current = fresh
    return nil
}
```

Key property: unmarshal into a temporary `fresh` first; only assign to
`Current` on success. Any failure leaves `Current` untouched → graceful
degradation is automatic, no special-casing at call sites.

`InitConfig()` is unchanged — it is still used for one-shot commands and for
the daemon's very first read.

### 2. `cmd/active.go` — `runDaemon` changes

- **Signature:** `runDaemon(client *glm.Client, force bool)` →
  `runDaemon(debug, force bool)`. The client is no longer constructed once
  outside the loop; each loop builds a fresh one that picks up the latest
  `config.Current` (api_key, base_url, proxy via the rebuilt transport).
- **Per-loop reload + rebuild:**

  ```go
  if err := config.Reload(); err != nil {
      log.Errorf("Config reload failed, keeping current: %v", err)
  }
  client := glm.NewClient()
  client.SetDebug(debug)
  ```

  Placed at the very top of the `for` loop body, before `Activate`. A failed
  reload logs an error and continues with the previous `Current`; the
  immediately following `glm.NewClient()` therefore still reads the unchanged
  `Current`. The client is never cached across loop iterations — building it
  fresh each iteration is what lets api_key/base_url/proxy changes take effect.

- **SIGHUP handler for immediate reload.** Register a separate
  `reloadCh` (kept distinct from `sigCh`, which carries only SIGINT/SIGTERM,
  so "shut down" and "reload" are unambiguous):

  ```go
  reloadCh := make(chan os.Signal, 1)
  signal.Notify(reloadCh, syscall.SIGHUP)
  ```

  Add a `case` to the sleep `select` so SIGHUP interrupts the current sleep
  and loops back to the top (which reloads and recomputes the next run):

  ```go
  select {
  case <-sigCh:
      log.Infof("Received signal, shutting down")
      return nil
  case <-reloadCh:
      log.Infof("Received SIGHUP, reloading config")
      continue
  case <-time.After(wait):
  }
  ```

  The reload actually happens at the top of the next iteration; this `case`
  just breaks out of the sleep early.

`RunE` for `activeCmd`: the one-shot path keeps constructing its own client as
today; only the service-mode call changes to `runDaemon(debug, force)`.

### 3. `cmd/reload.go` — new `glm reload` subcommand (new file)

A thin command that forwards SIGHUP to the systemd-managed daemon. It reuses
the existing `systemctlUser` helper from `install.go`.

```go
var reloadCmd = &cobra.Command{
    Use:   "reload",
    Short: "Reload daemon config without restarting",
    Long: `Send SIGHUP to the running systemd GLM daemon so it re-reads
config.yaml and recomputes the next activation time.

Only affects the systemd-managed daemon (installed via 'glm install').
For ad-hoc runs, send SIGHUP manually: kill -HUP <pid>.`,
    Args: cobra.NoArgs,
    RunE: func(cmd *cobra.Command, args []string) error {
        if err := systemctlUser("kill", "--signal=SIGHUP", serviceUnit); err != nil {
            return fmt.Errorf("reload %s: %w\n(Is the service installed and running? Run 'glm install' first.)", serviceUnit, err)
        }
        ui.Success(fmt.Sprintf("Sent reload signal to %s", serviceUnit))
        return nil
    },
}

func init() { rootCmd.AddCommand(reloadCmd) }
```

Notes:
- `systemctl --user kill --signal=SIGHUP glm.service` sends the signal without
  stopping/restarting the unit. Distinct from `systemctl reload`, which would
  require a dedicated `ExecReload=` in the unit file; using a raw signal keeps
  the unit file unchanged.
- If the service is not running, `systemctl` returns a non-zero exit; the
  error message steers the user toward `glm install`.

### 4. Command registration

`reload.go`'s `init()` calls `rootCmd.AddCommand(reloadCmd)`. No other wiring
needed (cobra discovers it via package init, same pattern as `install.go`).

### 5. Incidental cleanup

`cmd/active.go` has a duplicate log line — the `log.Infof("Activated — %d%%
remaining", quota.Remaining)` appears at both line 76 and line 83. Remove the
first (line 76); the second, after the fresh-status fetch, is the accurate one.

## Behavior Guarantees

| Scenario | Effect |
|---|---|
| Edit `schedule.times` then `glm reload` | SIGHUP interrupts current sleep; daemon reloads, recomputes next run from new times |
| Rotate `api_key` then `glm reload` | Next `Activate` uses the new key (client rebuilt) |
| Change `proxy` then `glm reload` | New proxy applied on next `Activate` (transport rebuilt with client) |
| Edit to malformed YAML then `glm reload` | `Reload()` errors, daemon keeps old config, logs error, continues |
| `glm reload` while service not running | `systemctl` fails; error message tells user to run `glm install` |
| SIGHUP arrives during an in-flight `Activate` | Signal is buffered in `reloadCh`; consumed when the loop reaches the sleep `select`; the in-flight request is not interrupted |
| No `glm reload` issued | Next loop iteration still re-reads disk at the top, so changes self-apply on the next cycle |

## Out of Scope (YAGNI)

- **`fsnotify` / `viper.WatchConfig`.** Per-loop disk re-read plus SIGHUP
  covers all realistic trigger points without wiring a file watcher into the
  blocking `select`. Adding one would complicate the main loop for no
  behavioral gain.
- **Support for ad-hoc (non-systemd) daemons in `glm reload`.** Those users
  can `kill -HUP <pid>` directly; the subcommand targets the installed
  systemd service, which is the project's deployment model.
- **Unit tests.** The project currently has no test suite. `Reload()` is a
  small pure-ish function; adding a one-off test would require scaffolding a
  test harness inconsistent with the rest of the codebase. Defaulting to no
  tests; can revisit if the user wants them.
- **README sync.** `README.md` is already out of sync with the actual command
  set (references `glm monitor`, `glm schedule`, `glm service` which don't
  exist). Fixing it is a separate concern.
- **Unit file changes.** No `ExecReload=` needed; the raw SIGHUP via
  `systemctl kill` keeps the generated unit file unchanged.
