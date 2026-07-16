# AKV 开发进度

更新：2026-07-16｜总体：`DONE`｜当前：无｜下一项：无

## 恢复点

- 回收、撤销、超时、取消、审计、180 天清理及崩溃 Lease 恢复均已通过真实 PostgreSQL 验证。
- Agent Bearer API 已装配 PostgreSQL，覆盖目标发现、begin/heartbeat/end task、申请与所有权隔离的状态查询。
- 人类登录/Session 已持久化；活动管理员初始化后可自助注册立即启用的无特权普通用户；Cookie 使用 HttpOnly/SameSite 且 HTTPS 默认 Secure，状态变更校验同源与双提交 CSRF。
- Web 已支持列表、注册、轮换/幂等撤销 Token 和启停自有 Agent；Token 只在注册/轮换当次返回，列表区分永久、过期和已撤销状态。
- 初始管理员命令只允许互动终端无回显输入两次密码；Web 管理员可列出已有用户并设置启停/`APPROVE_ALL`，唯一管理员不可变更。
- OpenBao 控制客户端已仅实现 KV/Transit Key/Database Role 配置写入，对外类型无读取、签名、动态签发或 Lease 撤销方法。
- 管理员目标/凭证 API 已支持录入、更新和停用；Vault 路径由服务端生成，秘密字节写入后清零且不返回路径。
- Web 已按 owner/APPROVE_ALL/管理员权限列出申请，展示冻结操作与风险，支持原子决策、撤销、审计链和 incident 关闭；关闭告警不恢复 Grant。
- 嵌入式 Web 工作台已覆盖登录、Agent Token 一次展示/变更、用户/目录管理、审批/撤销、审计和告警；交互表单使用统一 Vue 站内弹窗，业务数据仅用 textContent，无浏览器持久化。
- Agent 运行时使用 Bearer Token 直连 control 与 execution HTTP API；根目录 `CLAUDE.md` 强制 Claude Code 使用项目级 `akv-access` Skill 的固定客户端维持心跳、等待人工审批并只执行一次。
- `make verify-all` 使用全新临时 PostgreSQL 验证迁移、并发、race、不预置 Grant 的授权闭环及真实 Web 人类→Agent HTTP 行为链。
- 批准事务会锁定并重新校验 Agent 所属活动任务；任务已结束时不创建 Approval 或 Grant。
- 四项试点指标已有独立追加式产品采集链：历史申请保持未知，新申请明确区分零值；计数和复盘由可见人类显式记录，服务端时间汇总，不参与任何授权判断，真实值仍待试点且不预设改善幅度。
- 管理员 Web 已覆盖 HTTP/PostgreSQL 目标、全部 MVP 凭证类型、全局审计和安全告警；证书可存储但申请阶段禁止执行。
- 控制 API 绝不返回 credential vault_path、哈希、Lease 或任何秘密字段。
- Web 控制台工程位于根目录 `web/`，Vite 哈希产物输出到 `internal/control/web/dist/`，继续由 `akv-control` 单二进制同源交付，运行时不增加前端服务。

## 当前工作项

`AKV-023.a`（`DONE`）：为人工转交、审批补问和复盘起止增加独立追加式观测采集，并汇总四项试点指标；观测数据不参与审批、Grant、执行或回收判断。

验收条件：人类用户只能为其可见申请通过 Session/CSRF 记录受限事件；无自由文本和秘密字段；事件不可更新/删除；复盘会话防跨申请、重复完成和并发重复；真实 PostgreSQL 行为测试覆盖四项指标、权限隔离与未知/零区分；Web 可记录和查看汇总；`make verify-all` 与 `git diff --check` 通过。

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
| `AKV-016.a` | `DONE` | 015.a | 以 GitLab 项目查询替换本地健康接口演示教程 |
| `AKV-017.a` | `DONE` | 016.a | 本地 Claude Code 从 Git 忽略的根目录文件读取 Agent Token |
| `AKV-018.a` | `DONE` | 017.a | 现代站内表单弹窗与 Agent Token 幂等撤销 |
| `AKV-019.a` | `DONE` | 018.a | Claude Code 项目级 AKV Skill 与确定性直连客户端 |
| `AKV-020.a` | `DONE` | 019.a | GitLab 502 根因诊断与目标业务失败回归测试 |
| `AKV-021.a` | `DONE` | 020.a | 人类用户与 Agent 多参与者行为测试、隔离数据和测试报告 |
| `AKV-022.a` | `DONE` | 021.a | 四项试点观测指标的测试数据、统计口径和真实 PostgreSQL 可计算性验证 |
| `AKV-023.a` | `DONE` | 022.a | 追加式试点观测事件、四项指标汇总和真实人类/Agent 采集链 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`AKV-014.a` Vue 15 项测试、`make verify`、四个后端二进制构建、全包 race、真实临时 PostgreSQL、直连 API 与 Token 不回显测试、`git diff --check` 通过。
- 2026-07-15：`AKV-015.a` Vue 21 项测试与生产构建、`go vet`、全包单测/race、真实 PostgreSQL 迁移与 E2E、遗留原始请求默认拒绝、四个二进制构建和 `git diff --check` 通过。
- 2026-07-16：`AKV-016.a` GitLab 官方认证与项目 API 复核、HTTP/目录/代理单测、`make verify` 和 `git diff --check` 通过。
- 2026-07-16：`AKV-017.a` `.agent-token` Git 忽略/未跟踪检查、Vue 21 项测试与生产构建、`go vet`、全包单测和 `git diff --check` 通过。
- 2026-07-16：`AKV-018.a` Vue 32 项测试、桌面/移动端浏览器冒烟、`make verify`、真实 PostgreSQL 迁移与代理测试、`git diff --check` 通过。
- 2026-07-16：`AKV-019.a` Skill 结构校验、Node 客户端 3 项单测、真实本地只读目标发现、`make verify` 和 `git diff --check` 通过。
- 2026-07-16：`AKV-020.a` HTTP 502 单次执行/回收定向测试、Skill 业务结果 5 项单测、`make verify` 和 `git diff --check` 通过。
- 2026-07-16：`AKV-021.a` Vue 32 项、Agent 客户端 5 项、浏览器桌面/390px 冒烟、全包测试/race、真实 PostgreSQL store/proxy/behavior、`make verify-all` 和 `git diff --check` 通过。
- 2026-07-16：`AKV-022.a` 四项试点指标严格 fixture、合成计算、真实 PostgreSQL 申请到结果时间边界、Vue 32 项、Agent 客户端 5 项、全包测试/race、`make verify-all` 和 `git diff --check` 通过。
- 2026-07-16：`AKV-023.a` Vue 39 项、生产构建、全包测试/vet/race、真实 PostgreSQL store/proxy/behavior、追加不可变、权限隔离、精确时间边界及 12 路不同键并发完成验证通过；实现后 `make verify-all` 通过，审查修复后等价分项复验通过，`git diff --check` 通过。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-014.a`：移除 MCP 并以 CLAUDE.md 引导 Claude Code 安全直连 Agent Bearer HTTP API｜下一步无｜计划提交 `refactor(agent): remove MCP integration`
- 2026-07-15｜`AKV-015.a`：实现管理员发布/绑定的版本化安全操作、Agent 公开 Schema 发现和统一一次执行，升级时终结遗留原始请求｜下一步无｜计划提交 `feat(auth): add safe operation catalog`
- 2026-07-16｜`AKV-016.a`：用 GitLab 私有项目只读查询替换本地健康接口演示，补齐低权限令牌、审批执行、排错和撤销步骤｜下一步无｜计划提交 `docs(local): add GitLab target demo`
- 2026-07-16｜`AKV-017.a`：本地 Claude Code 改从根目录 `.agent-token` 读取身份 Token，并同步交付提示、教程、架构与安全规则｜下一步无｜计划提交 `docs(agent): read token from root file`
- 2026-07-16｜`AKV-018.a`：用统一 Vue 弹窗替换浏览器原生交互，区分 Token 状态并修复幂等撤销与错误映射｜下一步无｜计划提交 `fix(web): modernize dialogs and token revocation`
- 2026-07-16｜`AKV-019.a`：生成项目级 `akv-access` Skill 和固定 Node 客户端，消除临时请求脚本、错误心跳与执行格式猜测｜下一步无｜计划提交 `feat(agent): add deterministic AKV skill`
- 2026-07-16｜`AKV-020.a`：确认 GitLab 502 来自本机代理/DNS 链路，固化 HTTP 交换成功与目标业务失败的区分及一次性回收语义｜下一步无｜计划提交 `test(proxy): cover target bad gateway semantics`
- 2026-07-16｜`AKV-021.a`：新增真实人类/Agent HTTP 行为链与安全数据 manifest，修复终态任务仍可获批问题并生成报告｜下一步无｜计划提交 `test(behavior): cover human and agent journeys`
- 2026-07-16｜`AKV-022.a`：新增四项试点观测指标的合成计算、空真实值约束和 PostgreSQL 时间边界验证，不预设改善目标｜下一步无｜计划提交 `test(behavior): cover pilot observation metrics`
- 2026-07-16｜`AKV-023.a`：实现四项试点指标的受限追加式采集、Web 汇总、幂等重试、并发配对和真实人类/Agent 验证｜下一步无｜计划提交 `feat(observation): collect pilot metrics`

## MVP 验收

需求第 5 节 10 项均 `PASS`；逐项测试名、持久层与端到端证据见 `docs/acceptance.md`。
