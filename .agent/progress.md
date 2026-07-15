# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-008`｜下一项：`AKV-008.h`

## 恢复点

- 回收、撤销、超时、取消、审计、180 天清理及崩溃 Lease 恢复均已通过真实 PostgreSQL 验证。
- Agent Bearer API 已装配 PostgreSQL，覆盖目标发现、begin/heartbeat/end task、申请与所有权隔离的状态查询。
- 人类登录/Session 已持久化；Cookie 使用 HttpOnly/SameSite 且 HTTPS 默认 Secure，状态变更校验同源与双提交 CSRF。
- Web 已支持列表、注册、轮换/撤销 Token 和启停自有 Agent；Token 只在注册/轮换当次返回。
- 初始管理员命令只允许互动终端无回显输入两次密码；Web 管理员可列出已有用户并设置启停/`APPROVE_ALL`，唯一管理员不可变更。
- OpenBao 控制客户端已仅实现 KV/Transit Key/Database Role 配置写入，对外类型无读取、签名、动态签发或 Lease 撤销方法。
- 管理员目标/凭证 API 已支持录入、更新和停用；Vault 路径由服务端生成，秘密字节写入后清零且不返回路径。
- Web 已按 owner/APPROVE_ALL/管理员权限列出申请，展示冻结操作与风险，支持原子决策、撤销、审计链和 incident 关闭；关闭告警不恢复 Grant。
- 嵌入式 Web 工作台已覆盖登录、Agent Token 一次展示/变更、用户/目录管理、审批/撤销、审计和告警；业务数据仅用 textContent，无浏览器持久化。
- 下一轮 `AKV-008.h` 实现本地 AKV MCP Server 的完整工具集、0600 Token 文件和后台心跳。
- 控制 API 绝不返回 credential vault_path、哈希、Lease 或任何秘密字段。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-008.h / 实现 AKV MCP Server
验收条件：标准 stdio MCP initialize/tools/list/tools/call；list/get target、begin/heartbeat/end task、request/cancel/status/execute；Token 仅从 0600 文件自动注入且不在 schema/返回/错误中；begin 后 15 秒后台心跳；end/exit 停止心跳；控制与执行 HTTP 边界联调；无透明重试
修改范围：MCP 协议/客户端包、`cmd/akv-mcp-server`、Agent API 补齐、测试、memory/progress
验证命令：make verify；make test-migrations-postgres
风险 / 下一步：MCP 工具输入 schema 必须禁止 token/credential_id/任意 URL/认证头，进程日志只能走 stderr 且不记请求体
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `DONE` | 001 | 核心 schema、迁移机制及默认拒绝的状态转换 |
| `AKV-003` | `DONE` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `DONE` | 002 | 安全目标/凭证目录与 OpenBao 权限隔离 |
| `AKV-005` | `DONE` | 003,004 | 不可变申请、审批竞争、绑定 Grant 及 PostgreSQL 原子占用 |
| `AKV-006` | `DONE` | 005 | 独立受控代理、HTTP/PG/Transit、动态凭证与真实适配器 |
| `AKV-007` | `DONE` | 005,006 | 超时、撤销、回收、异常恢复、审计及保留清理 |
| `AKV-008` | `IN_PROGRESS` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`、catalog/control/store/vault race、`git diff --check` 和 `make test-migrations-postgres` 通过；目标/凭证更新停用、服务端 Vault 路径和无秘密 DTO 已验证。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-007.d`：实现状态触发审计、关联链与 180 天限量实际清理｜下一步 `AKV-007.e`｜计划提交 `feat(audit): persist safe lifecycle events`
- 2026-07-15｜`AKV-007.e`：持久化 Lease 并恢复超时/失败回收直至关闭 incident｜下一步 `AKV-008.a`｜计划提交 `feat(worker): recover interrupted executions`
- 2026-07-15｜`AKV-008.a`：装配无秘密 DTO 的 Agent Bearer 控制 API 与真实 PG 链路｜下一步 `AKV-008.b`｜计划提交 `feat(control): expose agent control API`
- 2026-07-15｜`AKV-008.b`：实现可撤销 Web Session、安全 Cookie 与同源/CSRF 边界｜下一步 `AKV-008.c`｜计划提交 `feat(control): authenticate web sessions`
- 2026-07-15｜`AKV-008.c`：实现自有 Agent 列表/注册/启停及 Token 一次返回的轮换/撤销｜下一步 `AKV-008.d`｜计划提交 `feat(control): manage owned agents`
- 2026-07-15｜`AKV-008.d`：实现无回显 bootstrap 和已有用户启停/APPROVE_ALL，保护唯一管理员｜下一步 `AKV-008.e`｜计划提交 `feat(control): manage human users`
- 2026-07-15｜`AKV-008.e1`：实现无读取能力的 OpenBao KV/Database Role 控制写客户端｜下一步 `AKV-008.e2`｜计划提交 `feat(vault): add control-plane writer`
- 2026-07-15｜`AKV-008.e2`：实现服务端路径的目标/凭证录入更新停用与无秘密 Web DTO｜下一步 `AKV-008.f`｜计划提交 `feat(control): manage credential catalog`
- 2026-07-15｜`AKV-008.f`：实现权限隔离的申请查询、原子决策/撤销、审计链和不恢复 Grant 的 incident 处置｜下一步 `AKV-008.g`｜计划提交 `feat(control): expose approval workspace`
- 2026-07-15｜`AKV-008.g`：实现无持久化/无不安全渲染的嵌入式人类审批工作台与 CSP｜下一步 `AKV-008.h`｜计划提交 `feat(web): add human control console`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
