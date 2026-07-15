# AKV 开发进度

更新：2026-07-15｜总体：`DONE`｜当前：无｜下一项：无

## 恢复点

- 回收、撤销、超时、取消、审计、180 天清理及崩溃 Lease 恢复均已通过真实 PostgreSQL 验证。
- Agent Bearer API 已装配 PostgreSQL，覆盖目标发现、begin/heartbeat/end task、申请与所有权隔离的状态查询。
- 人类登录/Session 已持久化；活动管理员初始化后可自助注册立即启用的无特权普通用户；Cookie 使用 HttpOnly/SameSite 且 HTTPS 默认 Secure，状态变更校验同源与双提交 CSRF。
- Web 已支持列表、注册、轮换/撤销 Token 和启停自有 Agent；Token 只在注册/轮换当次返回。
- 初始管理员命令只允许互动终端无回显输入两次密码；Web 管理员可列出已有用户并设置启停/`APPROVE_ALL`，唯一管理员不可变更。
- OpenBao 控制客户端已仅实现 KV/Transit Key/Database Role 配置写入，对外类型无读取、签名、动态签发或 Lease 撤销方法。
- 管理员目标/凭证 API 已支持录入、更新和停用；Vault 路径由服务端生成，秘密字节写入后清零且不返回路径。
- Web 已按 owner/APPROVE_ALL/管理员权限列出申请，展示冻结操作与风险，支持原子决策、撤销、审计链和 incident 关闭；关闭告警不恢复 Grant。
- 嵌入式 Web 工作台已覆盖登录、Agent Token 一次展示/变更、用户/目录管理、审批/撤销、审计和告警；业务数据仅用 textContent，无浏览器持久化。
- Agent 运行时使用 Bearer Token 直连 control 与 execution HTTP API；根目录 `CLAUDE.md` 约束 Claude Code 维持 15 秒心跳、等待人工审批并只执行一次。
- `make verify-all` 使用全新临时 PostgreSQL 验证迁移、并发、race 和不预置 Grant 的完整授权→执行→回收→审计闭环。
- 管理员 Web 已覆盖 HTTP/PostgreSQL 目标、全部 MVP 凭证类型、全局审计和安全告警；证书可存储但申请阶段禁止执行。
- 控制 API 绝不返回 credential vault_path、哈希、Lease 或任何秘密字段。
- Web 控制台工程位于根目录 `web/`，Vite 哈希产物输出到 `internal/control/web/dist/`，继续由 `akv-control` 单二进制同源交付，运行时不增加前端服务。

## 当前工作项

已完成工作项：

```text
ID / 目标：AKV-015.a / Agent 通过动态安全操作目录申请受控执行
验收：管理员可发布不可变操作版本并复用绑定到目标；Agent 只发现公开 Schema，并以 operation_id/version/arguments 申请；Control 严格校验并冻结私有模板生成的执行快照；原始 operation 请求、跨目标/版本、停用操作、Schema 绕过默认拒绝；一次性 Grant、凭证隔离、回收和审计语义保持不变；Web、Claude Code 引导和本地教程同步；完整验证通过并本地提交
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
| `AKV-012.a` | `DONE` | 011.b | MVP 普通用户账号密码自助注册 |
| `AKV-013.a` | `DONE` | 012.a | 本地 Claude Code 连接 MCP 并经人工审批执行完整样例的教程 |
| `AKV-014.a` | `DONE` | 013.a | 移除 MCP，Agent 持 Bearer Token 直连 HTTP API，并用 CLAUDE.md 引导 Claude Code |
| `AKV-015.a` | `DONE` | 014.a | 管理员安全操作目录、版本化目标绑定与 Agent 动态 Schema 申请 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`AKV-011.a` Vue 6 项测试、浏览器冒烟、`make verify`、全包 race、真实临时 PostgreSQL、`make build`、`git diff --check` 通过。
- 2026-07-15：`AKV-011.b` Vue 安全扫描与 6 项测试、Vite 构建、`make verify`、五个二进制构建和 `git diff --check` 通过。
- 2026-07-15：`AKV-012.a` Vue 15 项测试、浏览器注册/退出、`make verify`、race、真实 PostgreSQL 注册并发/回滚、`make build` 和 `git diff --check` 通过。
- 2026-07-15：`AKV-013.a` Claude Code 2.1.209 隔离配置的 MCP 命令实测、Base64 响应复核、`make verify` 和 `git diff --check` 通过。
- 2026-07-15：`AKV-014.a` Vue 15 项测试、`make verify`、四个后端二进制构建、全包 race、真实临时 PostgreSQL、直连 API 与 Token 不回显测试、`git diff --check` 通过。
- 2026-07-15：`AKV-015.a` Vue 21 项测试与生产构建、`go vet`、全包单测/race、真实 PostgreSQL 迁移与 E2E、遗留原始请求默认拒绝、四个二进制构建和 `git diff --check` 通过。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-008.g`：实现无持久化/无不安全渲染的嵌入式人类审批工作台与 CSP｜下一步 `AKV-008.h`｜计划提交 `feat(web): add human control console`
- 2026-07-15｜`AKV-008.h`：实现 9 工具 stdio MCP、0600 Token 注入、15 秒心跳和无重试受控执行｜下一步 `AKV-009.a`｜计划提交 `feat(mcp): expose controlled agent tools`
- 2026-07-15｜`AKV-009.a`：补齐本地运行/分权策略、完整 Web 控制面、actor/拒绝审计和真实 PG E2E｜下一步无｜计划提交 `test(e2e): verify MVP security matrix`
- 2026-07-15｜`AKV-010.a`：修复 hidden 被组件 display 覆盖导致登录后工作台不可见，并增加回归测试｜下一步无｜计划提交 `fix(web): honor hidden view state`
- 2026-07-15｜`AKV-011.a`：将完整控制台迁移为 Vue 3 + Vite，保留单二进制与安全边界并更新本地教程｜下一步无｜计划提交 `feat(web): migrate console to Vue`
- 2026-07-15｜`AKV-011.b`：将 Vue 工程迁移至根目录并保留 Go 嵌入产物边界｜下一步无｜计划提交 `refactor(web): separate source from embedded assets`
- 2026-07-15｜`AKV-012.a`：实现立即启用且固定无特权的账号密码自助注册、原子 Session 和可归因审计｜下一步无｜计划提交 `feat(web): add account registration`
- 2026-07-15｜`AKV-013.a`：重写本地 Claude Code 的 MCP 连接、人工批准和一次性执行样例｜下一步无｜计划提交 `docs(local): explain Claude Code MCP demo`
- 2026-07-15｜`AKV-014.a`：移除 MCP 并以 CLAUDE.md 引导 Claude Code 安全直连 Agent Bearer HTTP API｜下一步无｜计划提交 `refactor(agent): remove MCP integration`
- 2026-07-15｜`AKV-015.a`：实现管理员发布/绑定的版本化安全操作、Agent 公开 Schema 发现和统一一次执行，升级时终结遗留原始请求｜下一步无｜计划提交 `feat(auth): add safe operation catalog`

## MVP 验收

需求第 5 节 10 项均 `PASS`；逐项测试名、持久层与端到端证据见 `docs/acceptance.md`。
