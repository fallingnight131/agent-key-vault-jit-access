# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-006`｜下一项：`AKV-006.b`

## 恢复点

- HTTP 受控执行已实现先占用、固定目标、代理注入、禁止重定向/重试、30 秒超时和多形式脱敏清零。
- 下一轮 `AKV-006.b` 实现 PostgreSQL 单语句与预声明事务批次连接器，支持固定和 OpenBao 动态凭证。
- 单语句 60 秒、事务 5 分钟；仅参数化参数，批次任一步失败回滚，动态 Lease 必须撤销。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-006.b / 实现 PostgreSQL 受控执行链
验收条件：单语句/事务批次预声明；参数化执行；60 秒/5 分钟；失败回滚；动态签发失败零连接且无固定回退；Lease 终态撤销；make verify 通过
修改范围：PG 执行编排与连接器、数据库/vault fake、测试、memory/progress
验证命令：make verify
风险 / 下一步：不得把动态用户名/密码放入 DSN 日志或错误；连接工厂用敏感回调构造内存配置
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `DONE` | 001 | 核心 schema、迁移机制及默认拒绝的状态转换 |
| `AKV-003` | `DONE` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `DONE` | 002 | 安全目标/凭证目录与 OpenBao 权限隔离 |
| `AKV-005` | `DONE` | 003,004 | 不可变申请、审批竞争、绑定 Grant 及 PostgreSQL 原子占用 |
| `AKV-006` | `IN_PROGRESS` | 005 | HTTP 执行链已完成；待 PostgreSQL 与动态凭证 |
| `AKV-007` | `BACKLOG` | 005,006 | 超时、撤销、回收、告警、审计及 180 天清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`、代理包 `go test -race` 和 `git diff --check` 通过；占用拒绝时 Vault/目标零调用、成功仅一次注入、反射秘密脱敏清零且重定向未跟随。

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
- 2026-07-15｜`AKV-006.a`：实现先占用的 HTTP 注入、固定目标、无重试/重定向及多形式脱敏清零｜下一步 `AKV-006.b`｜计划提交 `feat(proxy): execute guarded HTTP operations`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
