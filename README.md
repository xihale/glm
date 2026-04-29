# glm

GLM API quota monitor and activation tool.

## Commands

```bash
glm login [name]          # add/update account; prompts for name if omitted, then API key
glm list                  # list accounts
glm logout <name>         # remove account
glm monitor [--debug]     # show quota
glm active [-f] [--debug] # activate quota
glm schedule set +8 09:00 # set daily activation schedule
glm schedule show         # show schedule
glm schedule clear        # clear schedule
glm service install       # install systemd user service/timer
glm service run           # run one activation cycle; logs mode=auto or mode=schedule
glm service stop          # stop and disable systemd user timer
glm service uninstall     # remove systemd user service/timer
```

No daemon/crontab mode. Scheduling is handled by systemd user timers.

## Config

Default path: `~/.config/glm/config.yaml`

```yaml
providers:
  - name: work
    type: glm
    api_key: sk-xxxxx
    enabled: true

schedule:
  timezone: +8
  times:
    - "09:00:00"
```

Use custom config:

```bash
glm --config ./config.yaml login work
glm --config ./config.yaml schedule set Asia/Shanghai 09:00
glm --config ./config.yaml service install
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
