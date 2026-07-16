# AKV 行为测试报告

- 日期：2026-07-16
- 变更前基线：`9b80ea9`
- 结论：核心授权安全行为与四项试点指标测试契约 `PASS`；真实试点基线仍待采集且未预设改善幅度；发现并修复 1 个任务终态审批缺陷，另有 6 项后续风险。

## 测试范围

本轮把此前分散的单元测试串成两个参与者视角的真实行为旅程：

1. 普通用户通过 Web 注册并注册 Agent；Agent 发现安全操作、开始任务、发送心跳、申请授权；人类使用 Session Cookie 和 CSRF 批准；Agent 仅执行一次并读取关联审计。
2. Agent 在等待审批时结束任务；人类随后尝试批准，系统必须原子拒绝且不得创建 Approval 或 Grant。
3. 以显式合成事件验证申请到结果用时、人工转交次数、审批补问次数和单次复盘用时的计算，并从完整行为旅程的 PostgreSQL 时间戳验证第一项可实际推导。

系统测试使用临时 PostgreSQL、真实 control/execution HTTP handler、进程内受保护目标和内存 Vault fake。没有连接真实 OpenBao、真实目标系统或真实凭证。

## 测试数据

声明式场景位于 `internal/behavior/testdata/actor-journeys.json`，只保存角色、检查点和预期实体数量。试点指标契约位于 `internal/behavior/testdata/pilot-metrics.json`，真实值全部为 `null`，改善目标为 `null`；其中的数值案例明确标记为 `SYNTHETIC_TEST_SPECIFICATION`，只用于验证计算逻辑。密码、Session、CSRF、Agent Token 与保护值均在测试进程运行时随机生成，不写入文件、日志或报告。

| 场景 | 用户 | Agent | 任务 | 申请 | Approval | Grant | 执行 | 回收 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 人工批准后仅执行一次 | 3 | 2 | 2 | 1 | 1 | 1 | 1 | 1 |
| 任务结束后拒绝批准 | 2 | 1 | 1 | 1 | 0 | 0 | 0 | 0 |

临时数据库在清理前必须同时满足：测试 Socket 位于 `/tmp` 且带专用前缀、数据库名与用户为测试身份、服务端 Socket 配置精确匹配、必要表存在。任一条件不满足即拒绝执行清理。

### 合成指标样本

下表不是试点结果或改进基线，只是固定的计算测试向量：

| 指标 | 合成输入的预期结果 |
| --- | ---: |
| 申请到结果用时 | 180,000 ms |
| 人工转交次数 | 2 次 |
| 审批补问次数 | 1 次 |
| 复盘一次操作所需时间 | 90,000 ms |

## 试点观测指标

“申请到结果”在本报告中明确指执行结果完成，不与审批决定时间或回收完成时间混用。当前授权数据库无需修改：第一项已有可靠时间边界，另外三项没有领域事件，不能从审计 actor 切换、重复申请或 HTTP 服务端耗时猜测。

| 指标 | 统计口径 / 数据源 | 本轮验证 | 真实试点值 | 改善幅度 |
| --- | --- | --- | --- | --- |
| 申请到结果用时 | `executions.completed_at - authorization_requests.created_at` | PASS：真实临时 PostgreSQL 完成链区间存在且非负 | 待试点 | 未预设 |
| 人工转交次数 | 显式 `manual_handoff` 事件计数 | PASS：合成事件计数；产品采集待试点 | 待试点 | 未预设 |
| 审批补问次数 | 显式 `approval_followup` 事件计数 | PASS：合成事件计数；产品采集待试点 | 待试点 | 未预设 |
| 复盘一次操作所需时间 | 同一复盘会话 `review_completed - review_started` | PASS：合成时间边界计算；产品采集待试点 | 待试点 | 未预设 |

未知值与明确观测为 0 严格区分。未来如需在产品内采集后三项，应另行设计不参与审批、Grant 占用或执行判断的追加式观测事件；本轮没有改动核心 schema 或安全语义。

## 行为结果

| 行为 | 结果 | 证据 |
| --- | --- | --- |
| 普通用户注册后保持无管理员、无全局审批权限 | PASS | Web 注册响应、Session/Cookie 行为与数据库 manifest |
| Agent 发现接口不返回目标地址、Vault 引用或私有执行模板 | PASS | HTTP 响应泄漏扫描 |
| 未审批时执行被拒绝 | PASS | HTTP 403；Vault 读取 0 次，目标调用 0 次 |
| 缺少 CSRF 的人工审批被拒绝 | PASS | HTTP 403；申请仍待审批 |
| 所有者批准自己的 Agent 请求 | PASS | HTTP 200；状态返回已批准 Grant 与到期时间 |
| 无关用户不可见、不可读另一用户审计 | PASS | 列表为空；审计 HTTP 404 |
| 跨 Agent、跨任务使用被拒绝 | PASS | HTTP 403；Vault/目标仍为 0 次 |
| 正确上下文只执行一次并脱敏响应 | PASS | Vault 读取 1 次，目标调用 1 次；反射值被替换 |
| 已完成申请的申请到结果用时可推导 | PASS | PostgreSQL 申请创建与执行完成时间边界完整且区间非负 |
| 重放被拒绝 | PASS | HTTP 403；累计 Vault/目标调用仍各 1 次 |
| request→approval→grant→execution→reclaim 审计链可读 | PASS | 关联审计事件完整且无运行时秘密 |
| 已结束任务不能被后续批准 | PASS | HTTP 409；Approval/Grant 均为 0 |

## 发现并修复

### F-001：已结束任务仍可创建 Grant

初次运行新回归时，Agent 将任务结束为 `CANCELLED` 后，人类审批仍返回 HTTP 200，数据库产生 1 条 Approval 和 1 条 Grant。执行守卫仍会因任务非活动而拒绝 Claim，因此未造成凭证访问，但会形成不可执行的僵尸 Grant 与错误审计记录。

修复在 PostgreSQL 的原子审批条件中加入任务所有权和 `ACTIVE` 状态校验，并在批准事务内锁定活动任务行，使并发任务结束必须在 Grant 创建后完成回收。修复后相同旅程返回 HTTP 409，且 Approval、Grant、Vault 读取和目标调用均为 0。

## 验证记录

环境：Go 1.26.5、Node.js 23.11.0、npm 11.16.0、PostgreSQL 17.10，macOS arm64。

| 命令/检查 | 结果 |
| --- | --- |
| `make verify-all` | PASS |
| Vue 安全扫描与组件测试 | 5 个文件、32 项 PASS |
| 固定 Agent 客户端测试 | 5 项 PASS，已纳入 `make verify` |
| Go 全包测试与 `go vet` | PASS |
| Go 全包 race | PASS |
| 真实 PostgreSQL store 测试 | PASS，10.146s |
| 真实 PostgreSQL proxy E2E | PASS，1.438s |
| 真实 PostgreSQL 人类/Agent 行为与时间边界测试 | PASS，5.178s |
| 四项试点指标严格 fixture 与合成计算测试 | 2 项 PASS |
| 浏览器注册表单与 390px 响应式冒烟 | PASS，无横向溢出 |
| `git diff --check` | PASS |

## 后续风险

以下问题没有阻断本轮核心授权行为验收，但应进入后续测试与修复队列：

1. `中`：Agent 注册和 Token 轮换界面目前固定为 24 小时，未提供架构定义的 30 天和永久选项及永久风险提示。
2. `中`：Web 退出请求失败时仍切回登录页，刷新后可能因服务端 Session 尚有效而恢复登录，界面会产生“已安全退出”的错觉。
3. `中`：多个进程并发执行数据库迁移时没有 PostgreSQL advisory lock，空库首次启动可能发生 DDL 竞争。
4. `中`：全局审计和申请审计页面未展示 approval、grant、execution、reclaim 的全部关联 ID。
5. `低`：审批竞争、撤销失败和目录失败仍可能直接显示内部错误码，缺少面向人类的可行动提示。
6. `高`：旧 PostgreSQL 测试在脱离官方临时数据库脚本单独运行时仍直接信任测试 DSN；新行为测试已增加强门禁，但旧测试尚未统一迁移到安全 fixture。

## 复现

运行完整验证：

```sh
GOCACHE=/tmp/akv-go-cache make verify-all
```

官方脚本会创建并销毁临时 PostgreSQL 数据目录和 Socket。不要把真实数据库连接信息传给测试环境变量。
