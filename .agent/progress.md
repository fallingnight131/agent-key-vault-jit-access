# AKV 开发进度

更新：2026-07-15｜总体：`DONE`｜当前：无｜下一项：无

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
- AKV MCP Server 已提供 9 个 stdio 工具，Token 仅从 0600 文件注入，无重试/重定向；任务按 15 秒自动心跳，结束或进程退出停止。
- `make verify-all` 使用全新临时 PostgreSQL 验证迁移、并发、race 和不预置 Grant 的完整授权→执行→回收→审计闭环。
- 管理员 Web 已覆盖 HTTP/PostgreSQL 目标、全部 MVP 凭证类型、全局审计和安全告警；证书可存储但申请阶段禁止执行。
- 控制 API 绝不返回 credential vault_path、哈希、Lease 或任何秘密字段。
- Web 控制台工程位于根目录 `web/`，Vite 哈希产物输出到 `internal/control/web/dist/`，继续由 `akv-control` 单二进制同源交付，运行时不增加前端服务。

## 当前工作项

已完成工作项：

```text
ID / 目标：AKV-011.b / 优化 Web 项目目录结构
结果：Vue 源码、测试和 Node 工具链已迁移到根目录 `web/`；Vite 继续输出到 Go embed 目录；构建、文档和路径引用已同步
验证：Vue 安全扫描与 6 项测试、Vite 生产构建、`make verify`、五个二进制构建和 `git diff --check` 通过
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
| `AKV-008` | `DONE` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `DONE` | 008 | 需求第 5 节全部端到端安全验收与演示 |
| `AKV-010.a` | `DONE` | 009 | 修复 Web 登录视图切换并增加回归测试 |
| `AKV-011.a` | `DONE` | 010.a | Vue 3 控制台、构建链、测试与本地运行教程 |
| `AKV-011.b` | `DONE` | 011.a | 前端源码与生成资源职责分离的项目结构 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`AKV-011.a` Vue 6 项测试、浏览器冒烟、`make verify`、全包 race、真实临时 PostgreSQL、`make build`、`git diff --check` 通过。
- 2026-07-15：`AKV-011.b` Vue 安全扫描与 6 项测试、Vite 构建、`make verify`、五个二进制构建和 `git diff --check` 通过。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-008.d`：实现无回显 bootstrap 和已有用户启停/APPROVE_ALL，保护唯一管理员｜下一步 `AKV-008.e`｜计划提交 `feat(control): manage human users`
- 2026-07-15｜`AKV-008.e1`：实现无读取能力的 OpenBao KV/Database Role 控制写客户端｜下一步 `AKV-008.e2`｜计划提交 `feat(vault): add control-plane writer`
- 2026-07-15｜`AKV-008.e2`：实现服务端路径的目标/凭证录入更新停用与无秘密 Web DTO｜下一步 `AKV-008.f`｜计划提交 `feat(control): manage credential catalog`
- 2026-07-15｜`AKV-008.f`：实现权限隔离的申请查询、原子决策/撤销、审计链和不恢复 Grant 的 incident 处置｜下一步 `AKV-008.g`｜计划提交 `feat(control): expose approval workspace`
- 2026-07-15｜`AKV-008.g`：实现无持久化/无不安全渲染的嵌入式人类审批工作台与 CSP｜下一步 `AKV-008.h`｜计划提交 `feat(web): add human control console`
- 2026-07-15｜`AKV-008.h`：实现 9 工具 stdio MCP、0600 Token 注入、15 秒心跳和无重试受控执行｜下一步 `AKV-009.a`｜计划提交 `feat(mcp): expose controlled agent tools`
- 2026-07-15｜`AKV-009.a`：补齐本地运行/分权策略、完整 Web 控制面、actor/拒绝审计和真实 PG E2E｜下一步无｜计划提交 `test(e2e): verify MVP security matrix`
- 2026-07-15｜`AKV-010.a`：修复 hidden 被组件 display 覆盖导致登录后工作台不可见，并增加回归测试｜下一步无｜计划提交 `fix(web): honor hidden view state`
- 2026-07-15｜`AKV-011.a`：将完整控制台迁移为 Vue 3 + Vite，保留单二进制与安全边界并更新本地教程｜下一步无｜计划提交 `feat(web): migrate console to Vue`
- 2026-07-15｜`AKV-011.b`：将 Vue 工程迁移至根目录并保留 Go 嵌入产物边界｜下一步无｜计划提交 `refactor(web): separate source from embedded assets`

## MVP 验收

需求第 5 节 10 项均 `PASS`；逐项测试名、持久层与端到端证据见 `docs/acceptance.md`。
