# glm

GLM (ChatGLM) API 配额监控与心跳保活工具。

## 功能

- **配额监控** — 查看剩余配额及重置时间
- **心跳激活** — 发送心跳请求以刷新配额
- **守护模式** — 通过 crontab 或 systemd 自动调度
- **多账户** — 同时管理多个 GLM API Key
- **代理支持** — HTTP/SOCKS 代理

## 安装

```bash
go install github.com/xihale/glm@latest
```

或从源码构建：

```bash
git clone https://github.com/xihale/glm.git
cd glm
go build -o glm .
./glm install
```

`glm install` 将二进制复制到 `~/.local/bin/glm`。

## 快速开始

```bash
# 设置 API Key
glm auth set glm

# 查看配额
glm monitor

# 激活（发送心跳）
glm activate

# 启动守护进程
glm daemon
```

## 命令

### 认证

```bash
glm auth set glm              # 交互式输入 API Key
glm auth list                 # 列出所有 provider
glm auth enable <name>        # 启用
glm auth disable <name>       # 禁用
glm auth remove <name>        # 删除
```

### 监控

```bash
glm monitor                   # 查看配额状态
glm monitor --debug           # 显示原始 API 响应
```

### 激活

```bash
glm activate                  # 发送心跳
glm activate --force          # 强制激活（即使配额充足）
glm activate --debug          # 调试输出
```

### 守护进程

```bash
glm daemon                    # 执行一次激活并调度下次运行
glm stop                      # 停止定时任务
```

### systemd 服务

```bash
glm service install           # 安装 systemd 用户服务
systemctl --user daemon-reload
systemctl --user enable --now glm

glm service uninstall         # 卸载服务
```

### 配置

```bash
glm config set proxy http://127.0.0.1:1080
glm config set glm.<name>.api_key sk-xxxxx
glm config set glm.<name>.base_url https://open.bigmodel.cn
```

### Shell 补全

```bash
glm completion bash --install
glm completion zsh --install
glm completion fish --install
```

## 配置文件

路径：`~/.config/glm/config.yaml`

```yaml
proxy: http://127.0.0.1:1080

# 单账户
glm:
  api_key: sk-xxxxx

# 多账户
providers:
  - name: work
    type: glm
    api_key: sk-xxxxx
    enabled: true
  - name: personal
    type: glm
    api_key: sk-yyyyy
    enabled: true
```

## License

[MIT](LICENSE)
