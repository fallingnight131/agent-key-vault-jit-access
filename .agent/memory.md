# AKV 持久记忆

只记录已确认、跨轮次有用且未在需求/架构中容易查到的结论。未确认问题放入 `progress.md`；禁止记录任何秘密。

## 权威文档

- 产品范围与验收：`docs/project-requirements.md`（默认只读）。
- 模块、时序、状态和信任边界：`docs/architecture.md`。
- 自主开发与提交规则：`AGENTS.md`。

## 关键决策索引

| 主题 | 已确认结论 |
| --- | --- |
| 人类权限 | 唯一管理员；`APPROVE_ALL` 为附加权限；普通用户审批自己的 Agent；MVP 不划分用户目标权限 |
| Agent Token | 每 Agent 一个有效 Token；24 小时/一个月/永久；重签立即撤销旧 Token；本地 MVP 从根目录、Git 忽略且 `0600` 的 `.agent-token` 读取，但不得放入 Prompt、请求 JSON 或日志 |
| 任务 | 服务端生成 UUIDv7；心跳 15 秒、失联 45 秒；退出保留 Agent Token，只回收任务授权 |
| 审批与 Grant | 申请理由必填；审批等待 30 分钟；批准时任务必须仍为 ACTIVE；批准后默认 10 分钟内开始；首次最终审批原子生效；Grant 一次占用 |
| OpenBao | KV v2 存固定凭证和证书；Transit 代签名；DB Engine 生成/撤销 PostgreSQL 动态凭证；证书只存储 |
| 执行 | HTTP 30 秒；收到目标 4xx/5xx 也表示交换完成，但业务结果失败；PG 单语句 60 秒；PG 事务批次 5 分钟；不透明重试；撤销仅保证阻止未发出操作并尽力取消在途操作 |
| 回收与审计 | 正常终态 5 秒内开始回收；回收失败 5 秒内告警；审计保留并实际清理 180 天 |

详细语义以架构文档为准，本表只用于快速定位，不应继续扩写架构摘要。

## 当前工程事实

- 2026-07-15：Git 已初始化在 `main`，首次提交包含需求、架构、Agent 文档和安全 `.gitignore`；用户未授权 push、发布或部署。
- 2026-07-15：后端采用 Go 1.26，优先使用标准库和显式接口；依据：类型与并发工具、单二进制部署及无外部依赖的快速验证；影响：新增依赖需有明确模块价值。
- 2026-07-15：统一验证入口为 `make verify`，包含 Vue 安全扫描/组件测试/生产构建、Go 格式、`go vet` 和全部 Go 测试；构建缓存使用 `/tmp/akv-go-cache` 以兼容受限环境。
- 2026-07-15：首个部署入口为 `cmd/akv-control`，默认仅监听 `127.0.0.1:8080`；执行代理后续必须保持独立进程和权限边界。
- 2026-07-15：PostgreSQL 迁移内嵌于 `internal/store/migrations`，按版本和 SHA-256 校验和原子应用；`make test-migrations-postgres` 使用临时本地实例做真实语法验证。
- 2026-07-15：业务库只保存密码/Token 哈希和 OpenBao 引用；初始 schema 以唯一索引保证单管理员、单 Agent 未撤销 Token，以触发器冻结授权申请快照。
- 2026-07-15：任务、申请、Grant、执行和回收状态转换集中在 `internal/domain`；状态均为默认拒绝，终态不可回退，Grant 执行结果必须进入回收。
- 2026-07-15：人类密码使用 `golang.org/x/crypto/bcrypt` 默认成本；Session 使用 256 位随机值且业务库只接收 SHA-256 哈希，身份错误不区分未知用户和错误密码。
- 2026-07-15：Agent Bearer Token 为 256 位随机值，支持 24 小时、30 天和永久三档；持久层只接收 SHA-256 哈希，轮换通过单个仓储事务接口撤销旧值并写入新值。
- 2026-07-15：任务 ID 由服务端生成 UUIDv7；心跳建议间隔 15 秒，Worker 在 45 秒边界原子转为 `AGENT_LOST` 并返回待回收任务，不修改 Agent Token。
- 2026-07-15：目标连接配置使用 HTTP/PostgreSQL 强类型白名单，不接受认证头、URL userinfo 或账号字段；授权申请只提交 `target_id`，服务端解析活动默认凭证。
- 2026-07-15：OpenBao 能力按进程拆为控制面仅写接口与执行面读取/Transit/动态签发/Lease 撤销接口；敏感值格式化恒为 `[REDACTED]` 且支持原地清零，动态签发失败绝不回退静态读取。
- 2026-07-15：授权申请只接受 `task_id`、`target_id`、`operation_id`、精确版本、参数和理由；服务端从活动绑定解析凭证与私有模板，严格校验参数后冻结真实执行快照，并绑定 Agent/任务/目标配置版本/凭证/操作定义哈希。
- 2026-07-15：审批服务只做权限/输入准备，最终竞争由仓储 `DecidePending` 单事务完成；批准同事务创建最长 10 分钟且绑定完整快照的 Grant，拒绝和过期不创建 Grant。
- 2026-07-15：执行守卫只依赖单个 `ClaimApproved` 条件更新能力，完整匹配 Grant/Agent/任务/目标/凭证/操作哈希/期限后才返回；不具备 Vault 或连接器能力。
- 2026-07-15：PostgreSQL 授权仓储用 serializable 事务原子写审批+Grant；占用用单条联结 `ACTIVE` task 的条件 `UPDATE ... RETURNING`，pgx v5 驱动和临时真实 PostgreSQL race 测试验证并发单赢家。
- 2026-07-15：HTTP 执行链固定服务端目标并禁止重定向/重试，30 秒超时、1 MiB 响应上限；先占用 Grant 再读 Vault，代理注入认证，响应对原值、URL/Base64/Basic 形式脱敏后返回并清零材料。
- 2026-07-15：PostgreSQL 执行链仅执行冻结的参数化语句；单语句 60 秒、事务批次 5 分钟且失败回滚；动态角色失败或配置要求动态但元数据不符时零连接/零静态回退，终态关闭连接并撤销 Lease。
- 2026-07-15：Transit 签名执行只把已批准摘要交给 OpenBao `Sign`，占用失败时调用次数为零，返回值仅为签名而无私钥读取路径。
- 2026-07-15：执行计划由 PostgreSQL 联结冻结申请、Grant、目标和凭证元数据加载；Execution 与 Grant 终态在同一事务同步；独立 `akv-execution-proxy` 默认监听 `127.0.0.1:8081`。
- 2026-07-15：OpenBao 执行客户端只接受 group/other 不可访问的 Token 文件，后端错误体不传播；pgx 目标工厂以结构化配置设置短生命周期用户名/密码，不把秘密拼入 DSN。
- 2026-07-15：execution-proxy 路由只接受 `request_id`/`task_id`，先用 PostgreSQL 哈希 Token 仓储认证 Agent；数据库 DSN 与 OpenBao Token 均从 group/other 不可访问文件加载，进程装配不包含控制面 Vault 写能力。
- 2026-07-15：已占用执行的所有结果通过统一 5 秒 finalizer 进入 `RECLAIMING`；清理失败原子落 `RECLAIM_FAILED` 并创建 OPEN incident，Grant 永不恢复，静态材料只清内存不删除 Vault 源值。
- 2026-07-15：Worker 每 5 秒原子过期 30 分钟申请/到期未占用 Grant，并在 45 秒心跳边界结束任务、撤销未消费 Grant；主动撤销与占用由条件更新竞争，执行中写 `revoked_at` 并产生取消 ID。
- 2026-07-15：execution-proxy 每秒轮询已撤销 EXECUTING Grant，用进程内 registry 取消活动 context；HTTP/PG/Sign 映射为 `CANCELLED` 后仍回收，正常 end_task 同事务撤销未完成 Grant。
- 2026-07-15：业务状态触发器写入仅含状态码的关联审计，应用审计 metadata 为严格白名单；Worker 按 1000 条批次实际删除 180 天前事件。
- 2026-07-15：动态执行只额外持久化不可返回的 OpenBao Lease 引用；Worker 按操作超时恢复卡死执行，`RECLAIM_FAILED` 可重试但 Grant 不恢复，成功后关闭既有 incident。
- 2026-07-15：控制服务从 `0600` DSN 文件连接并迁移 PostgreSQL；Agent API 仅以 Bearer Token 认证，对外 DTO 排除默认凭证、Vault 引用和内部连接地址，申请状态按 Agent 所有权查询。
- 2026-07-15：Web Session 固定 8 小时，业务库只存 SHA-256 哈希；Session Cookie 为 HttpOnly + SameSite=Strict，HTTPS 公开源默认 Secure，变更路由同时校验 Origin 和可读 CSRF Cookie/请求头。
- 2026-07-15：Web 用户只能列出和变更 owner 匹配的 Agent；Agent Token 只在注册/轮换响应返回一次，列表仅含过期时间；已撤销 Token 可在行锁保护下重新生成。
- 2026-07-15：初始管理员通过独立 CLI 从互动 TTY 无回显双次读取密码，不接受密码参数/环境/文件；MVP Web 可在活动管理员初始化后自助注册立即启用的普通用户，注册原子创建哈希账号与 Session，固定无管理员和 `APPROVE_ALL`；注册审计只关联新用户 ID 与状态，不含用户名或密码材料；改密仍不在 MVP。
- 2026-07-15：OpenBao 控制客户端是独立导出类型，只满足 `ControlWriter` 的 KV v2、不可导出 Transit Key 和 Database Role 写方法；凭证读取、签名、动态签发和 Lease 撤销仅存于执行客户端。
- 2026-07-15：管理 API 忽略客户端 Vault 路径并按凭证 UUID 生成 KV/Transit/Database 引用；秘密以 base64 JSON 字节输入并在请求后清零，对外目录 DTO 仅含别名/类型/状态而无 Vault 引用。
- 2026-07-15：Web 申请查询在 SQL 中按 Agent owner/APPROVE_ALL/管理员限定，返回冻结 operation、别名和非秘密风险提示；审批/撤销复用原子服务，人工关闭 incident 只变更告警状态且 Grant 保持 `RECLAIM_FAILED`。
- 2026-07-15：人类工作台使用 Vue 3 + Vite；可维护工程位于根目录 `web/`，生产产物输出到 `internal/control/web/dist/`，再由 Go embed 与控制 API 同源交付；源码扫描禁止 `v-html`、浏览器持久化、直接 HTML 写入和 console，CSP 禁止外部脚本/对象/框架，Agent Token 只在可清零的一次性 dialog 状态中展示。
- 2026-07-15：管理员以 HTTP/PostgreSQL/Sign 操作集复用安全子集，发布不可变操作版本并将精确版本绑定到目标；Agent 发现 API 只返回公开 Schema，不返回私有模板、凭证或 Vault 引用。
- 2026-07-16：Agent 使用 Bearer Token 直连 control 与 execution HTTP API；本地 Claude Code 由 `CLAUDE.md` 强制使用项目级 `akv-access` Skill 的固定 Node 客户端，从根目录、Git 忽略且 `0600` 的 `.agent-token` 读取 Token，并确定性处理精确 Origin、严格 JSON 心跳、动态发现、人工审批和统一路由一次执行；正式产品不沿用文件交付，目标源凭证仍只进入 execution proxy。
- 2026-07-16：HTTP 代理收到任何目标 HTTP 响应（包括 4xx/5xx）都以 `EXECUTION_SUCCEEDED` 记录交换完成并回收一次性 Grant；Agent 客户端另以目标状态码标记业务成功或失败。当前 HTTP 客户端继承 Go 默认环境代理，内部域名需由 execution proxy 所在环境的代理或直连 DNS 正确解析。
- 2026-07-15：`make verify-all` 是完整交付门，包含静态检查、全包 race 和全新临时 PostgreSQL；E2E 不预置申请/Grant，贯通 Agent、任务、审批、代理、回收、拒绝重放与 actor 审计。
- 2026-07-15：业务审计对申请/审批/主动撤销/拒绝 Claim 记录固定 USER/AGENT actor 和无敏感 metadata；唯一管理员可在 Web 查看最新 500 条全局审计。

## Agent 维护区

技术栈、目录结构、统一验证命令和已验证陷阱在实际建立后按下列格式追加；只保留当前有效结论：

后续仅追加经代码、测试或用户决定确认的当前有效结论。
