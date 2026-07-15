# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-002`｜下一项：`AKV-002.a`

## 恢复点

- Go 1.26 工程、控制服务入口、配置校验、健康检查及 `make verify` 已建立。
- 下一轮 `AKV-002.a` 从架构数据模型提炼迁移，先实现数据库 schema 与可重复的迁移测试。
- 当前实现无第三方依赖；引入 PostgreSQL 驱动前先以 SQL 迁移和持久层接口固定领域约束。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-002.a / 建立核心数据库 schema 与迁移机制
验收条件：覆盖架构核心实体和状态约束；迁移可重复执行测试；make verify 通过
修改范围：数据库迁移、迁移执行器与测试、memory/progress
验证命令：make verify
风险 / 下一步：并发安全约束必须落在 PostgreSQL；不得把凭证明文列加入业务表
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `IN_PROGRESS` | 001 | 领域状态、数据库 schema/migration |
| `AKV-003` | `BACKLOG` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `BACKLOG` | 002 | 目标/凭证目录与 OpenBao 集成 |
| `AKV-005` | `BACKLOG` | 003,004 | 申请、审批竞争、一次性 Grant 原子占用 |
| `AKV-006` | `BACKLOG` | 005 | 受控代理、脱敏、HTTP/PG 连接器、动态凭证 |
| `AKV-007` | `BACKLOG` | 005,006 | 超时、撤销、回收、告警、审计及 180 天清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`（格式、`go vet`、全部 Go 测试）及 `git diff --check` 通过。

## 最近循环（最多 10 条）

- 2026-07-15｜文档基线：建立并精简自主循环、记忆和进度规则｜下一步 `AKV-001`｜计划提交 `docs(agent): establish autonomous workflow`
- 2026-07-15｜`AKV-001.a`：建立 Git 与安全 `.gitignore`，提交项目文档基线｜下一步 `AKV-001.b`｜提交 `chore(repo): establish AKV MVP baseline`
- 2026-07-15｜`AKV-001.b`：建立 Go 控制服务骨架、健康检查和统一验证入口｜下一步 `AKV-002.a`｜计划提交 `feat(control): bootstrap control service`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
