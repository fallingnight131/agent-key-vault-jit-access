# AKV MVP 验收证据

权威清单为 `docs/project-requirements.md` 第 5 节。下面每项必须同时通过 `make verify-all`；单元测试证明边界语义，`TestPostgreSQL*` 测试证明并发状态落在持久层。

`TestPostgreSQLEndToEndAuthorizationFlow` 不预置申请或 Grant：它使用真实 PostgreSQL 完成 Agent 注册与认证、任务、目录解析、冻结申请、人工审批、代理执行、静态材料内存销毁、回收、重放拒绝和关联审计。受保护目标与值均为测试进程内 fixture。

| # | 验收项 | 可复现证据 |
| --- | --- | --- |
| 1 | 未人工批准无法使用凭证 | `TestHTTPProxyClaimsBeforeVaultAndTarget`、`TestSignDeniedDoesNotCallTransit`、`TestClaimRejectsExpiredRevokedAndInactiveTask`；拒绝时 Vault/Transit/目标调用均为零 |
| 2 | 获批后只能执行一次指定操作 | `TestClaimRejectsReplayAndConcurrentUse`、`TestPostgreSQLAuthorizationConcurrency`；32 路内存竞争和 24 路真实 PG 竞争均只有一个赢家 |
| 3 | Prompt、上下文、工具参数、日志和错误不泄露凭证 | `TestSensitiveValueRedactsFormattingAndDestroys`、`TestHTTPProxyInjectsOnceAndRedactsReflectedSecret`、`TestAuditRejectsSensitiveOrArbitraryMetadata`、`TestPostgreSQLAuditChainAndRetention`、`TestProtocolListsToolsWithoutTokenOrCredentialBypass`、`TestOpenBaoErrorBodyIsNeverReturned` |
| 4 | 操作完成后授权复用失败 | `TestClaimRejectsReplayAndConcurrentUse`、`TestPostgreSQLAuthorizationConcurrency`、`TestPostgreSQLEndToEndAuthorizationFlow`；重放拒绝同时写入无敏感数据的 actor 审计事件 |
| 5 | 复制、并发、跨 Agent、跨任务复用均拒绝 | `TestClaimRejectsEveryContextMismatch`、`TestClaimRejectsReplayAndConcurrentUse`、`TestAgentRevokeRequiresExactAgentBinding`、`TestPostgreSQLAuthorizationConcurrency` |
| 6 | 失败、取消、超时、Agent 退出后自动回收 | `TestHTTPProxyCancellationBecomesCancelledAndReclaimed`、`TestPostgreSQLBatchRollsBackAndRevokesLease`、`TestPostgreSQLLifecycleSweepAndRevoke`、`TestPostgreSQLTaskEndRevokesUnfinishedGrant`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant` |
| 7 | 审批人主动撤销后不能继续使用 | `TestPostgreSQLLifecycleSweepAndRevoke`、`TestRevokePermissions`；持久层撤销后 Claim 被拒绝，执行中产生取消投递 ID，并记录 USER/AGENT actor |
| 8 | 静态源凭证不会误删 | `TestStaticHandleDestroysMemoryWithoutDeletingSource`、`TestHTTPProxyInjectsOnceAndRedactsReflectedSecret`；内存销毁且 Lease 撤销调用为零 |
| 9 | 临时派生凭证正确销毁 | `TestDynamicHandleRevokesLeaseAndDestroysMaterial`、`TestPostgreSQLBatchRollsBackAndRevokesLease`、`TestPostgreSQLCleanupFailureBecomesReclaimFailure`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant` |
| 10 | 审计完整关联申请、审批、使用和回收 | `TestPostgreSQLEndToEndAuthorizationFlow`、`TestPostgreSQLAuditChainAndRetention`；真实 PG 中存在 request→approval→grant→execution→reclaim 全关联事件、申请/审批 actor，且验证 180 天限量清理 |

## 额外安全门

- 申请快照不可变与服务端默认凭证：`TestSubmitFreezesServerBoundSnapshot`、`TestCreateAndResolveServerDefaultCredential`、`TestPostgreSQLAuthorizationConcurrency` 的真实触发器拒绝。
- 目标不可绕过：`TestConnectionConfigRejectsCredentialBypass`、`TestAuthorizationRequestRejectsCredentialAndTargetBypassFields`。
- 动态 PostgreSQL 不降级固定凭证：`TestDynamicIssueFailureNeverFallsBackToStatic`、`TestPostgreSQLDynamicFailureHasNoConnectionOrFallback`。
- 回收失败永久阻断并告警：`TestPostgreSQLCleanupFailureBecomesReclaimFailure`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant`。
- Web Session/CSRF：`TestWebLoginUsesProtectedCookiesAndNoTokenBody`、`TestWebLogoutRequiresCSRFAndRevokesSession`。
- Web 自助注册：`TestRegisterCreatesActiveNonAdminSessionWithoutPersistingSecrets`、`TestWebRegisterCreatesOrdinarySessionWithProtectedCookies`、`TestPostgreSQLRegistrationRequiresAdminAndCreatesActiveSession`、`TestPostgreSQLConcurrentRegistrationAllowsOneUsername`；账号与 Session 原子创建，固定为无特权普通用户，同名并发只有一个成功。
- MCP Token 文件、无重试和后台心跳：`TestClientInjectsProtectedTokenWithoutRetryOrErrorLeak`、`TestBeginStartsAndEndStopsBackgroundHeartbeat`。
