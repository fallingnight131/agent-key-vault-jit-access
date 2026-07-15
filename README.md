# AKV MVP

AKV（Agent Key Vault）让已注册 Agent 申请一次性操作授权。人类在 Web 控制台审核冻结的操作；执行代理在 PostgreSQL 原子占用 Grant 后才访问 OpenBao，并代 Agent 调用目标系统。Agent、MCP 与 Web 都不会得到源凭证明文。

## 进程与边界

| 进程 | 默认地址 / 传输 | 允许持有的能力 |
| --- | --- | --- |
| `akv-control` | `127.0.0.1:8080` | PostgreSQL 控制面；OpenBao 只写 Token |
| `akv-execution-proxy` | `127.0.0.1:8081` | PostgreSQL 执行面；OpenBao 读/签名/动态签发/Lease 撤销 Token |
| `akv-worker` | 后台进程 | 超时、失联、取消投递、回收恢复、审计清理 |
| `akv-mcp-server` | stdio | 一个 Agent Token；无 OpenBao 或数据库访问 |
| `akv-bootstrap-admin` | 交互 TTY | 一次性创建唯一管理员 |

控制面与执行面必须使用不同的操作系统身份、OpenBao Token 和运行目录。参考策略位于 `deploy/openbao/`。Token、数据库 DSN 和其他敏感配置必须由部署环境直接放入 owner-only（`0600`）常规文件；不要通过命令行、环境变量、仓库文件或 shell 历史传入值。

## 构建与验证

需要 Go 1.26 和 PostgreSQL 工具（`initdb`、`pg_ctl`、`psql`）：

```sh
make verify
make test-migrations-postgres
make build
```

`make verify-all` 同时运行静态检查、race 测试和临时真实 PostgreSQL 集成。测试仅使用进程内 fixture，不连接真实 OpenBao 或目标系统。

## 本地启动

1. 创建 AKV 专用 PostgreSQL 数据库，并让部署系统把 DSN 写入各进程可读的 `0600` 文件。
2. 配置 OpenBao KV v2、Transit、Database Secrets Engine 与 Audit Device；分别签发 `control-policy.hcl` 和 `execution-policy.hcl` 对应的非 Root Token，并写入各自 `0600` 文件。
3. 复制 `.env.example` 中的非秘密地址和文件路径到进程管理器。生产公开源使用 HTTPS，并保持 `AKV_CONTROL_COOKIE_SECURE=true`。
4. 在交互终端执行 `bin/akv-bootstrap-admin -username <name>`；密码会无回显读取两次，不能从管道输入。
5. 启动 control、execution proxy 和 worker，浏览 `http://127.0.0.1:8080/`；管理员可录入 HTTP/PostgreSQL 目标、全部 MVP 凭证类型并查看全局审计，用户可注册 Agent 并立即保存只显示一次的 Token。
6. 让部署系统把该 Token 写入 MCP 专用 `0600` 文件，然后启动 `bin/akv-mcp-server`。stdout 专用于 MCP JSON-RPC。

所有进程收到 `SIGINT`/`SIGTERM` 后会停止；MCP 退出后心跳停止，Worker 在 45 秒边界将任务标为 `AGENT_LOST` 并撤销未完成授权。

## MCP 工具

`list_targets`、`get_target`、`begin_task`、`heartbeat_task`、`end_task`、`request_authorization`、`get_authorization_status`、`execute_authorized_operation`、`cancel_authorization_request`。

Token 不属于任何工具参数。操作输入只允许注册目标 ID、服务端任务 ID 和强类型 HTTP/PostgreSQL/签名参数；不能提交凭证 ID、任意目标 URL 或认证头。连接器不做透明重试，任何重试或追加操作都需要新审批。

## 运维注意

- Web 不提供凭证明文读取或导出。目标与凭证停用后，新申请默认拒绝。
- 回收失败会保持 Grant 阻断并创建告警；人工关闭告警不会恢复 Grant。Worker 会继续尝试可恢复的 Lease 清理。
- 静态源凭证只清理执行内存，不从 OpenBao 删除；动态凭证在每次终态撤销 Lease。
- OpenBao Audit Device 与 AKV 业务审计应同时启用。AKV 审计默认保留 180 天并由 Worker 实际清理。

逐项安全证据见 `docs/acceptance.md`。
