# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-004`｜下一项：`AKV-004.b`

## 恢复点

- 目标与凭证元数据目录已实现管理员写入、认证发现、强类型安全配置和服务端默认凭证解析。
- 下一轮 `AKV-004.b` 实现 OpenBao KV v2 写入、Transit 引用和动态数据库角色的窄接口与 fake。
- 控制面只允许写入源凭证，后续执行代理使用独立读取/签名/动态签发接口。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-004.b / 建立 OpenBao 权限隔离接口
验收条件：控制面写接口与执行面读/签名/动态签发接口分离；动态签发失败不降级；返回值不暴露给控制面；make verify 通过
修改范围：vault 接口、类型、fake/契约测试、memory/progress
验证命令：make verify
风险 / 下一步：不接触真实 OpenBao 或真实秘密；敏感值仅在执行边界短生命周期对象中出现
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `DONE` | - | Git、安全忽略规则、Go 工程、控制服务入口及统一验证 |
| `AKV-002` | `DONE` | 001 | 核心 schema、迁移机制及默认拒绝的状态转换 |
| `AKV-003` | `DONE` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `IN_PROGRESS` | 002 | 目标/凭证目录已完成；待 OpenBao 权限隔离接口 |
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

- 2026-07-15：`make verify` 和 `git diff --check` 通过；目录测试覆盖管理员边界、认证发现、默认凭证绑定、停用/错配和配置旁路拒绝。

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

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
