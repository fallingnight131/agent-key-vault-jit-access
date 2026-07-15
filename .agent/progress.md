# AKV 开发进度

更新：2026-07-15｜总体：`IN_PROGRESS`｜当前：`AKV-001`｜下一项：`AKV-001.b`

## 恢复点

- Git 和安全 `.gitignore` 已建立，需求、架构及 Agent 文档已纳入首次基线提交。
- 仓库仍无技术栈、实现和测试入口；下一轮 `AKV-001.b` 选择最小连贯技术栈，建立工程骨架及 `make verify`。
- 技术栈应优先满足类型/并发安全、PostgreSQL/OpenBao 集成、MCP 接入、可测试性和 MVP 速度；若选择昂贵或难替换的基础设施，先询问用户。

## 当前工作项

下一最小切片：

```text
ID / 目标：AKV-001.b / 建立可运行工程骨架
验收条件：记录技术栈决策；提供最小服务入口和测试；make verify 通过
修改范围：工程配置、最小源码、测试、Makefile、memory/progress
验证命令：由所选技术栈确定，最终统一为 make verify
风险 / 下一步：避免一次引入多个重叠框架；完成后进入 AKV-002
```

## 队列

| ID | 状态 | 依赖 | 交付结果 |
| --- | --- | --- | --- |
| `AKV-001` | `IN_PROGRESS` | - | Git 与 `.gitignore` 已完成；待工程和统一验证入口 |
| `AKV-002` | `BACKLOG` | 001 | 领域状态、数据库 schema/migration |
| `AKV-003` | `BACKLOG` | 002 | 人类身份、Agent Token、任务与心跳 |
| `AKV-004` | `BACKLOG` | 002 | 目标/凭证目录与 OpenBao 集成 |
| `AKV-005` | `BACKLOG` | 003,004 | 申请、审批竞争、一次性 Grant 原子占用 |
| `AKV-006` | `BACKLOG` | 005 | 受控代理、脱敏、HTTP/PG 连接器、动态凭证 |
| `AKV-007` | `BACKLOG` | 005,006 | 超时、撤销、回收、告警、审计及 180 天清理 |
| `AKV-008` | `BACKLOG` | 003-007 | MCP 工具和 Web 控制面 |
| `AKV-009` | `BACKLOG` | 008 | 需求第 5 节全部端到端安全验收与演示 |

工作前可把一项拆成 `AKV-NNN.a` 等最小提交；任何时刻只有一个 `IN_PROGRESS`。

## 待决/阻塞

- 技术栈未确认：由 `AKV-001` 选择并写入 memory；只有重大且难逆选择才阻塞询问。
- 部署环境未确认：先提供本地、可重复、无真实凭证的运行方式。
- 当前无真实阻塞。

## 最近验证

- 2026-07-15：文档围栏和常见凭证模式检查通过；`.gitignore` 命中本地凭证、运行数据及构建产物；尚无代码测试。

## 最近循环（最多 10 条）

- 2026-07-15｜文档基线：建立并精简自主循环、记忆和进度规则｜下一步 `AKV-001`｜计划提交 `docs(agent): establish autonomous workflow`
- 2026-07-15｜`AKV-001.a`：建立 Git 与安全 `.gitignore`，提交项目文档基线｜下一步 `AKV-001.b`｜提交 `chore(repo): establish AKV MVP baseline`

## MVP 验收

以 `docs/project-requirements.md` 第 5 节为唯一清单。只有存在可复现测试或演示证据时，才在此追加 `PASS + 证据位置`；不要复制整张清单。
