# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-002`｜下一项：`AKV-002.b`

## 恢复点

- 核心 PostgreSQL schema、内嵌校验和迁移器和临时 PostgreSQL 集成测试已建立。
- 下一轮 `AKV-002.b` 建立领域状态与合法转换，优先覆盖任务、申请、Grant、执行和回收的不回退约束。
- 数据库连接驱动仍未引入；到控制服务真正装配持久层时选择维护活跃的 PostgreSQL 驱动。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-002.b / 实现领域状态和转换规则
验收条件：非法回退默认拒绝；覆盖任务、申请、Grant、执行和回收状态测试；make verify 通过
修改范围：领域状态类型、转换校验与测试、memory/progress
验证命令：make verify
风险 / 下一步：应用层校验不能替代后续 SQL 条件更新；转换规则须与架构状态图一致
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `IN_PROGRESS` | 001 | schema/migration 已完成；待领域状态转换 |
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

- 2026-07-15：`make verify`、`git diff --check` 和 `make test-migrations-postgres` 通过；真实 PostgreSQL 创建 14 张业务表及申请快照冻结触发器。

## 最近循环（最多 10 条）

- 2026-07-15｜文档基线：建立并精简自主循环、记忆和进度规则｜下一步 `AKV-001`｜计划提交 `docs(agent): establish autonomous workflow`
- 2026-07-15｜`AKV-001.a`：建立 Git 与安全 `.gitignore`，提交项目文档基线｜下一步 `AKV-001.b`｜提交 `chore(repo): establish AKV MVP baseline`
- 2026-07-15｜`AKV-001.b`：建立 Go 控制服务骨架、健康检查和统一验证入口｜下一步 `AKV-002.a`｜计划提交 `feat(control): bootstrap control service`
- 2026-07-15｜`AKV-002.a`：建立核心 schema、校验和迁移器及真实 PostgreSQL 验证｜下一步 `AKV-002.b`｜计划提交 `feat(store): add core database schema`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
