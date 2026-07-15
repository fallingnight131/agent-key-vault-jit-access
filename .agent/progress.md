# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-006`｜下一项：`AKV-006.a`

## 恢复点

- PostgreSQL 审批事务与单 SQL Grant 占用已通过真实数据库 race 测试，`AKV-005` 安全并发语义完成。
- 下一轮 `AKV-006.a` 实现独立执行编排、先占用后取 Vault、HTTP 凭证注入和响应/错误脱敏。
- 所有占用失败路径必须证明 Vault/目标调用为零；连接器默认不重试且 30 秒超时。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-006.a / 实现 HTTP 受控执行链
验收条件：原子占用先于 Vault；固定目标/方法；凭证仅代理注入；30 秒无重试；结果/日志/错误脱敏；完成后清零；make verify 通过
修改范围：执行代理服务、HTTP 连接器、脱敏器、fake/测试、独立命令入口、memory/progress
验证命令：make verify
风险 / 下一步：HTTP 响应可能反射秘密，脱敏必须覆盖原始值及常见编码；任何错误不得格式化敏感对象
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `DONE` | 001 | 核心 schema、迁移机制及默认拒绝的状态转换 |
| `AKV-003` | `DONE` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `DONE` | 002 | 安全目标/凭证目录与 OpenBao 权限隔离 |
| `AKV-005` | `DONE` | 003,004 | 不可变申请、审批竞争、绑定 Grant 及 PostgreSQL 原子占用 |
| `AKV-006` | `IN_PROGRESS` | 005 | 受控代理、脱敏、HTTP/PG 连接器、动态凭证 |
| `AKV-007` | `BACKLOG` | 005,006 | 超时、撤销、回收、告警、审计及 180 天清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`、`git diff --check` 和 `make test-migrations-postgres` 通过；真实 PostgreSQL 审批并发单赢家、24 路 Grant 占用单赢家且重放拒绝。

## 最近循环（最多 10 条）

- 2026-07-15｜文档基线：建立并精简自主循环、记忆和进度规则｜下一步 `AKV-001`｜计划提交 `docs(agent): establish autonomous workflow`
- 2026-07-15｜`AKV-001.a`：建立 Git 与安全 `.gitignore`，提交项目文档基线｜下一步 `AKV-001.b`｜提交 `chore(repo): establish AKV MVP baseline`
- 2026-07-15｜`AKV-001.b`：建立 Go 控制服务骨架、健康检查和统一验证入口｜下一步 `AKV-002.a`｜计划提交 `feat(control): bootstrap control service`
- 2026-07-15｜`AKV-002.a`：建立核心 schema、校验和迁移器及真实 PostgreSQL 验证｜下一步 `AKV-002.b`｜计划提交 `feat(store): add core database schema`
- 2026-07-15｜`AKV-002.b`：实现任务、申请、Grant、执行和回收的默认拒绝状态矩阵｜下一步 `AKV-003.a`｜计划提交 `feat(domain): enforce lifecycle transitions`
- 2026-07-15｜`AKV-003.a`：实现 bcrypt、唯一初始化、Session 哈希和人类权限核心服务｜下一步 `AKV-003.b`｜计划提交 `feat(identity): add human authentication core`
- 2026-07-15｜`AKV-003.b`：实现 Agent Token 三档有效期、哈希认证、原子轮换接口和停用｜下一步 `AKV-003.c`｜计划提交 `feat(agent): enforce token lifecycle`
- 2026-07-15｜`AKV-003.c`：实现 UUIDv7 任务、Agent 绑定、活动心跳和 45 秒失联扫描｜下一步 `AKV-004.a`｜计划提交 `feat(task): add managed task sessions`
- 2026-07-15｜`AKV-004.a`：实现管理员目录、认证发现、安全连接白名单和服务端默认凭证｜下一步 `AKV-004.b`｜计划提交 `feat(catalog): add safe target catalog`
- 2026-07-15｜`AKV-004.b`：隔离 OpenBao 控制/执行能力并实现敏感清零和动态无降级｜下一步 `AKV-005.a`｜计划提交 `feat(vault): isolate OpenBao capabilities`
- 2026-07-15｜`AKV-005.a`：实现活动任务校验、服务端凭证、强类型操作和不可变上下文哈希｜下一步 `AKV-005.b`｜计划提交 `feat(authz): create immutable requests`
- 2026-07-15｜`AKV-005.b`：实现审批权限、首个决定竞争及批准同事务 Grant｜下一步 `AKV-005.c`｜计划提交 `feat(authz): enforce approval competition`
- 2026-07-15｜`AKV-005.c`：实现完整上下文单调用占用契约及并发/重放安全测试｜下一步 `AKV-005.d`｜计划提交 `feat(authz): guard one-time grant claims`
- 2026-07-15｜`AKV-005.d`：实现 PostgreSQL 审批事务和单 SQL 占用并通过真实并发测试｜下一步 `AKV-006.a`｜计划提交 `feat(store): persist atomic authorization`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
