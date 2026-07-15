# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-006`｜下一项：`AKV-006.f`

## 恢复点

- OpenBao 执行客户端与 pgx 目标连接工厂已通过契约/race/真实 PostgreSQL 测试。
- 下一轮 `AKV-006.f` 实现 PostgreSQL Agent 认证仓储、0600 数据库配置文件和 HTTP/PG/Sign 受保护路由装配。
- 进程入口仍只有健康端点；受认证路由完成前不标记 `AKV-006`。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-006.f / 装配受认证执行路由
验收条件：数据库和 OpenBao Token 只从 0600 文件读取；Bearer Agent 认证；HTTP/PG/Sign 路由只用服务端计划；独立入口完整装配；make verify 通过
修改范围：Agent PostgreSQL 仓储、受保护路由、进程配置/装配、集成测试、memory/progress
验证命令：make verify
风险 / 下一步：路由不得接受目标/凭证/操作参数；请求体只允许 request_id 和 task_id
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `DONE` | 001 | 核心 schema、迁移机制及默认拒绝的状态转换 |
| `AKV-003` | `DONE` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `DONE` | 002 | 安全目标/凭证目录与 OpenBao 权限隔离 |
| `AKV-005` | `DONE` | 003,004 | 不可变申请、审批竞争、绑定 Grant 及 PostgreSQL 原子占用 |
| `AKV-006` | `IN_PROGRESS` | 005 | 核心执行/持久化/真实适配器已完成；待受保护路由 |
| `AKV-007` | `BACKLOG` | 005,006 | 超时、撤销、回收、告警、审计及 180 天清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`、代理/Vault race、`git diff --check` 和 `make test-migrations-postgres` 通过；OpenBao 错误脱敏和真实 pgx 连接已验证。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-004.b`：隔离 OpenBao 控制/执行能力并实现敏感清零和动态无降级｜下一步 `AKV-005.a`｜计划提交 `feat(vault): isolate OpenBao capabilities`
- 2026-07-15｜`AKV-005.a`：实现活动任务校验、服务端凭证、强类型操作和不可变上下文哈希｜下一步 `AKV-005.b`｜计划提交 `feat(authz): create immutable requests`
- 2026-07-15｜`AKV-005.b`：实现审批权限、首个决定竞争及批准同事务 Grant｜下一步 `AKV-005.c`｜计划提交 `feat(authz): enforce approval competition`
- 2026-07-15｜`AKV-005.c`：实现完整上下文单调用占用契约及并发/重放安全测试｜下一步 `AKV-005.d`｜计划提交 `feat(authz): guard one-time grant claims`
- 2026-07-15｜`AKV-005.d`：实现 PostgreSQL 审批事务和单 SQL 占用并通过真实并发测试｜下一步 `AKV-006.a`｜计划提交 `feat(store): persist atomic authorization`
- 2026-07-15｜`AKV-006.a`：实现先占用的 HTTP 注入、固定目标、无重试/重定向及多形式脱敏清零｜下一步 `AKV-006.b`｜计划提交 `feat(proxy): execute guarded HTTP operations`
- 2026-07-15｜`AKV-006.b`：实现参数化 PG 单语句/事务、超时回滚和动态凭证无降级 Lease 生命周期｜下一步 `AKV-006.c`｜计划提交 `feat(proxy): execute guarded PostgreSQL operations`
- 2026-07-15｜`AKV-006.c`：实现先占用的 Transit 摘要签名且无私钥读取路径｜下一步 `AKV-006.d`｜计划提交 `feat(proxy): execute guarded Transit signatures`
- 2026-07-15｜`AKV-006.d`：持久化冻结计划与 Execution 终态并建立独立执行进程｜下一步 `AKV-006.e`｜计划提交 `feat(store): persist execution lifecycle`
- 2026-07-15｜`AKV-006.e`：实现 0600 Token OpenBao 执行客户端与结构化 pgx 目标工厂｜下一步 `AKV-006.f`｜计划提交 `feat(proxy): add real execution adapters`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
