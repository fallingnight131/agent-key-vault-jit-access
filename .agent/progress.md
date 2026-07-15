# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-007`｜下一项：`AKV-007.c`

## 恢复点

- Worker 已实现申请/Grant 超时、45 秒失联、未消费撤销和执行中取消 ID；主动撤销权限与原子竞争已建立。
- 下一轮 `AKV-007.c` 将数据库取消请求投递到 execution-proxy 的活动 context，并补正常任务结束触发相同回收。
- 取消投递采用数据库轮询，不引入难替换消息基础设施；已完成业务结果不承诺回滚。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-007.c / 投递执行取消与任务结束回收
验收条件：活动执行注册 cancel；revoked_at/任务终止 5 秒内触发 context 取消；正常 end_task 撤销未完成授权；无活动执行安全幂等；make verify/真实 PG 通过
修改范围：取消注册表/轮询、代理集成、任务仓储、测试、memory/progress
验证命令：make verify
风险 / 下一步：跨进程取消只能尽力；代理崩溃由 Worker 的回收恢复路径兜底
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
| `AKV-007` | `IN_PROGRESS` | 005,006 | 回收/异常/超时/撤销已完成；待取消投递和审计清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：`make verify`、生命周期/仓储 race、`git diff --check` 和 `make test-migrations-postgres` 通过；真实 PG 验证撤销不可占用及三类超时/失联状态。

## 最近循环（最多 10 条）

- 2026-07-15｜`AKV-005.c`：实现完整上下文单调用占用契约及并发/重放安全测试｜下一步 `AKV-005.d`｜计划提交 `feat(authz): guard one-time grant claims`
- 2026-07-15｜`AKV-005.d`：实现 PostgreSQL 审批事务和单 SQL 占用并通过真实并发测试｜下一步 `AKV-006.a`｜计划提交 `feat(store): persist atomic authorization`
- 2026-07-15｜`AKV-006.a`：实现先占用的 HTTP 注入、固定目标、无重试/重定向及多形式脱敏清零｜下一步 `AKV-006.b`｜计划提交 `feat(proxy): execute guarded HTTP operations`
- 2026-07-15｜`AKV-006.b`：实现参数化 PG 单语句/事务、超时回滚和动态凭证无降级 Lease 生命周期｜下一步 `AKV-006.c`｜计划提交 `feat(proxy): execute guarded PostgreSQL operations`
- 2026-07-15｜`AKV-006.c`：实现先占用的 Transit 摘要签名且无私钥读取路径｜下一步 `AKV-006.d`｜计划提交 `feat(proxy): execute guarded Transit signatures`
- 2026-07-15｜`AKV-006.d`：持久化冻结计划与 Execution 终态并建立独立执行进程｜下一步 `AKV-006.e`｜计划提交 `feat(store): persist execution lifecycle`
- 2026-07-15｜`AKV-006.e`：实现 0600 Token OpenBao 执行客户端与结构化 pgx 目标工厂｜下一步 `AKV-006.f`｜计划提交 `feat(proxy): add real execution adapters`
- 2026-07-15｜`AKV-006.f`：装配 PostgreSQL Agent 认证、0600 配置和三类受保护执行路由｜下一步 `AKV-007.a`｜计划提交 `feat(proxy): assemble authenticated runtime`
- 2026-07-15｜`AKV-007.a`：统一 5 秒回收并将失败永久阻断为 incident｜下一步 `AKV-007.b`｜计划提交 `feat(lifecycle): enforce terminal reclaim`
- 2026-07-15｜`AKV-007.b`：实现撤销权限、申请/Grant 超时、45 秒失联和 Worker｜下一步 `AKV-007.c`｜计划提交 `feat(worker): sweep revocation and timeouts`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
