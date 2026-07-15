# AKV MVP 验收证据

权威清单为 `docs/project-requirements.md` 第 5 节。下面每项必须同时通过 `make verify-all`；单元测试证明边界语义，`TestPostgreSQL*` 测试证明并发状态落在持久层。

`TestPostgreSQLEndToEndAuthorizationFlow` 不预置申请或 Grant：它使用真实 PostgreSQL 完成 Agent 注册与认证、任务、可复用安全操作发布与目标精确版本绑定、公开 Schema 参数编译、冻结申请、人工审批、统一入口代理执行、静态材料内存销毁、回收、重放拒绝和关联审计。它还验证目标配置变化、操作停用和绑定换版会撤销或拒绝旧 Grant，且不访问 OpenBao 或目标。受保护目标与值均为测试进程内 fixture。

| # | 验收项 | 可复现证据 |
| --- | --- | --- |
| 1 | 未人工批准无法使用凭证 | `TestHTTPProxyClaimsBeforeVaultAndTarget`、`TestSignDeniedDoesNotCallTransit`、`TestClaimRejectsExpiredRevokedAndInactiveTask`；拒绝时 Vault/Transit/目标调用均为零 |
| 2 | 获批后只能执行一次指定操作 | `TestClaimRejectsReplayAndConcurrentUse`、`TestPostgreSQLAuthorizationConcurrency`、`TestPostgreSQLEndToEndAuthorizationFlow`；操作 ID/版本、定义哈希、目标配置版本和编译快照都受绑定，并只有一个并发占用者成功 |
| 3 | Prompt、上下文、工具参数、日志和错误不泄露凭证 | `TestSensitiveValueRedactsFormattingAndDestroys`、`TestHTTPProxyInjectsOnceAndRedactsReflectedSecret`、`TestAuditRejectsSensitiveOrArbitraryMetadata`、`TestPostgreSQLAuditChainAndRetention`、`TestAgentBearerAPILifecycleDoesNotEchoToken`、`TestAgentOperationDiscoveryReturnsOnlyPublicSchema`、`TestAuthorizationRequestRejectsCredentialAndTargetBypassFields`、`TestOpenBaoErrorBodyIsNeverReturned` |
| 4 | 操作完成后授权复用失败 | `TestClaimRejectsReplayAndConcurrentUse`、`TestPostgreSQLAuthorizationConcurrency`、`TestPostgreSQLEndToEndAuthorizationFlow`；重放拒绝同时写入无敏感数据的 actor 审计事件 |
| 5 | 复制、并发、跨 Agent、跨任务复用均拒绝 | `TestClaimRejectsEveryContextMismatch`、`TestClaimRejectsReplayAndConcurrentUse`、`TestAgentRevokeRequiresExactAgentBinding`、`TestPostgreSQLAuthorizationConcurrency` |
| 6 | 失败、取消、超时、Agent 退出后自动回收 | `TestHTTPProxyCancellationBecomesCancelledAndReclaimed`、`TestPostgreSQLBatchRollsBackAndRevokesLease`、`TestPostgreSQLLifecycleSweepAndRevoke`、`TestPostgreSQLTaskEndRevokesUnfinishedGrant`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant` |
| 7 | 审批人主动撤销后不能继续使用 | `TestPostgreSQLLifecycleSweepAndRevoke`、`TestRevokePermissions`；持久层撤销后 Claim 被拒绝，执行中产生取消投递 ID，并记录 USER/AGENT actor |
| 8 | 静态源凭证不会误删 | `TestStaticHandleDestroysMemoryWithoutDeletingSource`、`TestHTTPProxyInjectsOnceAndRedactsReflectedSecret`；内存销毁且 Lease 撤销调用为零 |
| 9 | 临时派生凭证正确销毁 | `TestDynamicHandleRevokesLeaseAndDestroysMaterial`、`TestPostgreSQLBatchRollsBackAndRevokesLease`、`TestPostgreSQLCleanupFailureBecomesReclaimFailure`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant` |
| 10 | 审计完整关联申请、审批、使用和回收 | `TestPostgreSQLEndToEndAuthorizationFlow`、`TestPostgreSQLAuditChainAndRetention`；真实 PG 中存在 request→approval→grant→execution→reclaim 全关联事件、申请/审批 actor，且验证 180 天限量清理 |

## 额外安全门

- 申请快照不可变与服务端默认凭证：`TestSubmitFreezesServerBoundSnapshot`、`TestCreateAndResolveServerDefaultCredential`、`TestPostgreSQLAuthorizationConcurrency` 的真实触发器拒绝。
- 安全操作目录：`TestAdministratorPublishesReusableOperationAndBindsTarget`、`TestPublishingCreatesNewImmutableVersionWithoutMovingBindings`、`TestOperationCatalogRejectsNonAdminAndUnsafeDefinitions`、`TestWebAdministratorPublishesAndBindsSafeOperationVersion`；操作集可复用，版本不可变，只有管理员可管理并必须显式绑定精确版本。
- Schema 与私有模板边界：`TestCompileHTTP`、`TestCompilePostgreSQL`、`TestCompileSign`、`TestInvalidSchemas`、`TestInvalidArguments`、`TestRejectsUnsafeHTTPTemplates`、`TestRejectsUnsafePostgreSQLTemplates`、`TestRejectsBindingAmplification`；原始操作申请由 `TestAuthorizationRequestRejectsLegacyRawOperation` 拒绝。
- 升级时遗留原始请求默认拒绝：`scripts/test-migrations-postgres.sh` 在应用安全操作迁移前插入待审批、已批准和执行中的旧记录，验证迁移分别终结、撤销和设置取消；`TestLegacyKindSpecificExecutionRoutesAreNotExposed` 验证旧执行路由返回 404。
- 配置和绑定变化默认拒绝：`TestPostgreSQLEndToEndAuthorizationFlow` 验证目标 `config_version` 变化、操作停用和绑定换版会阻断旧 Grant，OpenBao/目标调用为零。
- 目标不可绕过：`TestConnectionConfigRejectsCredentialBypass`、`TestAuthorizationRequestRejectsCredentialAndTargetBypassFields`。
- 动态 PostgreSQL 不降级固定凭证：`TestDynamicIssueFailureNeverFallsBackToStatic`、`TestPostgreSQLDynamicFailureHasNoConnectionOrFallback`。
- 回收失败永久阻断并告警：`TestPostgreSQLCleanupFailureBecomesReclaimFailure`、`TestPostgreSQLCrashRecoveryRetriesWithoutRestoringGrant`。
- Web Session/CSRF：`TestWebLoginUsesProtectedCookiesAndNoTokenBody`、`TestWebLogoutRequiresCSRFAndRevokesSession`。
- Web 自助注册：`TestRegisterCreatesActiveNonAdminSessionWithoutPersistingSecrets`、`TestWebRegisterCreatesOrdinarySessionWithProtectedCookies`、`TestPostgreSQLRegistrationRequiresAdminAndCreatesActiveSession`、`TestPostgreSQLConcurrentRegistrationAllowsOneUsername`；账号与 Session 原子创建，固定为无特权普通用户，同名并发只有一个成功。
- Agent 直连 Bearer API：`TestAgentBearerAPILifecycleDoesNotEchoToken`、`TestAgentAPIRequiresBearer`、`TestAgentOperationDiscoveryReturnsOnlyPublicSchema`、`TestExecutionRouteAuthenticatesAndAcceptsOnlyIdentifiers`、`TestExecutionRouteRejectsMissingBearerBeforeExecutor`；服务端不回显 Agent Token，control 和 execution 都独立认证，发现不泄露私有模板，统一执行 body 只接受 `request_id` 和 `task_id`。
