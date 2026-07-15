# AKV 本地运行教程

这篇教程用于在一台开发机上启动 AKV MVP。

完成后，本机会运行：

- PostgreSQL：保存 AKV 的业务数据；
- OpenBao：保存测试凭证；
- `akv-control`：Web 控制台和控制 API，默认端口 `8080`；
- `akv-execution-proxy`：受控执行服务，默认端口 `8081`；
- `akv-worker`：处理超时、回收和异常恢复，不监听端口；
- `akv-mcp-server`：由 Claude Code 等 MCP 客户端按需启动，通过标准输入输出通信。

> 本教程只适合本地开发。请使用专门创建的测试账号和测试凭证，不要使用生产凭证。

## 1. 准备软件

请先安装下面的软件：

- Go 1.26；
- Node.js 20.19+ LTS、22.13+ LTS 或 24+，以及 npm 10 或更高版本；
- PostgreSQL，并确保 `psql`、`createuser` 和 `createdb` 可以直接运行；
- OpenBao，并确保 `bao` 命令可以直接运行；
- `curl`，用于检查服务是否启动成功。

进入项目目录：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
```

检查命令是否可用：

```sh
go version
node --version
npm --version
psql --version
bao version
curl --version
```

## 2. 构建 AKV

Web 控制台使用 Vue 3 和 Vite。Vue 只参与构建，运行 AKV 时不需要单独启动前端服务。

执行：

```sh
make build
```

第一次构建时，`make build` 会通过 `npm ci` 安装锁定版本的前端依赖，运行前端构建，然后把生成的静态文件嵌入 `akv-control`。因此第一次构建需要能够访问 npm 软件源。

如果只修改了 Vue 前端，也可以先单独检查和构建：

```sh
make web-test
make web-build
```

完整的前端工程位于项目根目录 `web`，Vue 源码位于 `web/src`，前端测试位于 `web/test`。生成文件位于 `internal/control/web/dist`，只供 Go 服务嵌入使用，不要手工编辑。

构建成功后，`bin` 目录中会出现以下程序：

```text
akv-control
akv-execution-proxy
akv-worker
akv-mcp-server
akv-bootstrap-admin
```

## 3. 准备本地敏感配置目录

AKV 不接受通过命令参数直接传入数据库密码、OpenBao Token 或 Agent Token。这些值必须放在只有当前用户可以读取的普通文件中。

本教程使用 `/tmp/akv-local`。电脑重启后，这个目录可能被系统清理。

```sh
mkdir -p /tmp/akv-local/control /tmp/akv-local/execution /tmp/akv-local/agent
chmod 700 /tmp/akv-local /tmp/akv-local/control /tmp/akv-local/execution /tmp/akv-local/agent
install -m 600 /dev/null /tmp/akv-local/control/database-dsn
install -m 600 /dev/null /tmp/akv-local/control/openbao-token
install -m 600 /dev/null /tmp/akv-local/execution/openbao-token
install -m 600 /dev/null /tmp/akv-local/agent/token
```

不要在项目目录中保存这些文件，也不要提交它们。

## 4. 初始化 PostgreSQL

先确认 PostgreSQL 已经启动。然后创建一个本地测试用户：

```sh
createuser --pwprompt akv
```

命令会要求输入两次密码。请使用一个只用于本地开发的密码。

创建数据库：

```sh
createdb --owner=akv akv
```

使用文本编辑器打开下面的文件：

```text
/tmp/akv-local/control/database-dsn
```

写入一行 PostgreSQL DSN。格式如下：

```text
postgres://akv:你的本地测试密码@127.0.0.1:5432/akv?sslmode=disable
```

保存后再次确认权限：

```sh
chmod 600 /tmp/akv-local/control/database-dsn
```

AKV 启动时会自动执行数据库迁移，不需要手工导入 SQL。

如果本机 PostgreSQL 使用 Unix Socket 或其他端口，请按实际情况修改 DSN。

## 5. 启动和初始化 OpenBao

### 5.1 启动本地开发服务器

打开一个新终端，执行：

```sh
bao server -dev
```

这个终端需要一直保持运行。启动日志会显示开发服务器地址和 Root Token。

开发模式只用于本地测试。它的数据通常保存在内存中，停止后会丢失。

### 5.2 登录 OpenBao

再打开一个终端，进入项目目录：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
export BAO_ADDR=http://127.0.0.1:8200
bao login
```

出现提示后，粘贴开发服务器启动时显示的 Root Token。不要把 Root Token 写进命令参数、项目文件或 AKV 的运行配置。

### 5.3 启用 AKV 使用的秘密引擎

依次执行：

```sh
bao secrets enable -path=kv kv-v2
bao secrets enable transit
bao secrets enable database
```

如果提示某个路径已经存在，说明该引擎已经启用，可以继续下一步。

### 5.4 写入权限策略

执行：

```sh
bao policy write akv-control deploy/openbao/control-policy.hcl
bao policy write akv-execution deploy/openbao/execution-policy.hcl
```

这两个策略的权限不同：

- `akv-control` 只能写入或配置凭证，不能读取凭证；
- `akv-execution` 只能在执行阶段读取、签名、签发动态凭证或撤销 Lease。

### 5.5 创建两个运行 Token

把控制面 Token 直接写入受保护文件：

```sh
bao token create -orphan -policy=akv-control -field=token > /tmp/akv-local/control/openbao-token
chmod 600 /tmp/akv-local/control/openbao-token
```

把执行面 Token 写入另一个文件：

```sh
bao token create -orphan -policy=akv-execution -field=token > /tmp/akv-local/execution/openbao-token
chmod 600 /tmp/akv-local/execution/openbao-token
```

AKV 的业务进程只能使用这两个受限 Token，不能使用 Root Token。

## 6. 创建第一个管理员

打开一个新终端，进入项目目录，然后设置数据库配置文件的位置：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
export AKV_DATABASE_DSN_FILE=/tmp/akv-local/control/database-dsn
```

创建管理员：

```sh
bin/akv-bootstrap-admin -username admin
```

程序会要求输入并确认密码。密码输入时不会显示在终端中。

管理员只能初始化一次。如果再次运行时提示创建失败，请先尝试使用已经创建的管理员登录。

## 7. 启动 AKV 的三个后端进程

三个进程应分别放在三个终端中运行。

### 7.1 启动控制服务

在第一个终端执行：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
export AKV_DATABASE_DSN_FILE=/tmp/akv-local/control/database-dsn
export AKV_CONTROL_LISTEN_ADDRESS=127.0.0.1:8080
export AKV_CONTROL_PUBLIC_ORIGIN=http://127.0.0.1:8080
export AKV_CONTROL_COOKIE_SECURE=false
export AKV_OPENBAO_CONTROL_ADDRESS=http://127.0.0.1:8200
export AKV_OPENBAO_CONTROL_TOKEN_FILE=/tmp/akv-local/control/openbao-token
bin/akv-control
```

看到 `control service listening` 表示服务已经开始监听。

Web 静态资源已经嵌入这个进程。修改 Vue 源码后，需要重新执行 `make web-build` 或 `make build`，然后重启 `akv-control`。不需要另外启动 Vite 开发服务器。

### 7.2 启动执行代理

在第二个终端执行：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
export AKV_DATABASE_DSN_FILE=/tmp/akv-local/control/database-dsn
export AKV_EXECUTION_LISTEN_ADDRESS=127.0.0.1:8081
export AKV_OPENBAO_ADDRESS=http://127.0.0.1:8200
export AKV_OPENBAO_TOKEN_FILE=/tmp/akv-local/execution/openbao-token
bin/akv-execution-proxy
```

看到 `execution proxy listening` 表示服务已经开始监听。

### 7.3 启动后台 Worker

在第三个终端执行：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
export AKV_DATABASE_DSN_FILE=/tmp/akv-local/control/database-dsn
export AKV_OPENBAO_ADDRESS=http://127.0.0.1:8200
export AKV_OPENBAO_TOKEN_FILE=/tmp/akv-local/execution/openbao-token
bin/akv-worker
```

Worker 正常运行时可能长时间没有输出，这是正常现象。它每 5 秒检查一次超时、回收、异常恢复和审计清理任务。

## 8. 检查服务状态

打开另一个终端，执行：

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8081/healthz
```

两个命令都应该返回包含 `"status":"ok"` 的 JSON。

如果请求失败，请先检查对应进程的终端输出，再确认 `8080`、`8081` 和 `8200` 端口没有被其他程序占用。

## 9. 登录 Web 控制台

在浏览器中打开：

```text
http://127.0.0.1:8080/
```

可以使用第 6 步创建的管理员账号登录，也可以点击“注册账号”，输入用户名和密码创建普通账号。注册密码至少需要 8 个字符。普通账号注册成功后会直接进入工作台，但不会获得管理员权限或全局审批权限。

自助注册是 MVP 为本地体验保留的简化能力。普通用户可以注册自己的 Agent 并审批该 Agent 的申请，因此不要把这个版本的控制端直接暴露到不可信网络；正式产品应替换为企业身份系统和受控账号开通流程。

登录后可以：

- 管理用户；
- 注册自己的 Agent；
- 添加 HTTP 或 PostgreSQL 目标；
- 为目标配置测试凭证；
- 审批或拒绝 Agent 的授权申请；
- 查看审计记录和安全告警。

本地使用 HTTP，所以 `AKV_CONTROL_COOKIE_SECURE` 设置为 `false`。正式环境必须使用 HTTPS，并将它设置为 `true`。

## 10. 注册 Agent 并保存 Token

在 Web 控制台中注册一个 Agent。注册成功后，页面只会显示一次完整 Agent Token。

使用文本编辑器打开：

```text
/tmp/akv-local/agent/token
```

把 Token 写入文件，只保留 Token 本身，不要添加引号或其他内容。保存后执行：

```sh
chmod 600 /tmp/akv-local/agent/token
```

不要把 Agent Token 放进 Prompt、MCP 工具参数、环境变量或项目文件。

如果 Token 丢失，请在 Web 控制台中轮换 Token。轮换后，旧 Token 会立即失效。

## 11. 让本地 Claude Code 连接 AKV MCP

下面以本地命令行版 Claude Code 为例。下文使用的是 `claude` 命令，不是 Claude Desktop。

### 11.1 先理解启动方式

不要另开一个终端把 `akv-mcp-server` 当成普通后台服务运行。正确关系是：

```text
Claude Code <-- stdio --> akv-mcp-server <-- HTTP --> AKV 控制服务和执行代理
```

Claude Code 会自动启动 `akv-mcp-server` 子进程，并在会话结束时关闭它。`akv-mcp-server` 不监听端口，也不会打印“启动成功”。如果手工运行二进制后它一直安静等待，表示它正在等待 MCP 客户端从标准输入发送消息，不是卡死。

退出会话只会关闭当前 stdio 子进程，不会删除 MCP 配置。下次运行 `claude` 时，Claude Code 会按保存的配置重新启动它。

### 11.2 检查前置条件

先确认已安装 Claude Code，且前面步骤中的控制服务、执行代理和 Worker 都没有退出。然后执行：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new

claude --version
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8081/healthz
test -x bin/akv-mcp-server && echo "MCP 二进制：正常" || echo "MCP 二进制缺失，请先执行 make build"
test -s /tmp/akv-local/agent/token && echo "Agent Token 文件：正常" || echo "Agent Token 文件缺失或为空"
chmod 600 /tmp/akv-local/agent/token
```

两个 `curl` 都应返回包含 `"status":"ok"` 的 JSON，后面两项检查都应显示“正常”。任何一项失败时都不要继续。如果第一条命令提示 `claude: command not found`，请先安装 Claude Code，确认 `claude --version` 可用后再回到本节。

### 11.3 把 AKV MCP 添加到 Claude Code

在项目根目录执行：

```sh
claude mcp add --scope local akv \
  -e AKV_CONTROL_URL=http://127.0.0.1:8080 \
  -e AKV_EXECUTION_URL=http://127.0.0.1:8081 \
  -e AKV_AGENT_TOKEN_FILE=/tmp/akv-local/agent/token \
  -- "$(pwd)/bin/akv-mcp-server"
```

这条命令的意思是：

- `akv` 是这个 MCP Server 在 Claude Code 中的名字；
- `--scope local` 使配置只在本机当前用户下、对这个项目生效，不会写入可提交的 `.mcp.json`；
- `$(pwd)` 会在添加配置时变成当前项目的绝对路径；
- `AKV_AGENT_TOKEN_FILE` 传的是 Token 文件路径，不是 Token 内容。

`AKV_CONTROL_URL` 和 `AKV_EXECUTION_URL` 可以省略，两者的默认值分别就是上面的 `8080` 和 `8081`。这里显式写出是为了让演示配置更容易检查。`AKV_AGENT_TOKEN_FILE` 必须设置。

如果 Claude Code 提示已经存在名为 `akv` 的配置，先查看它：

```sh
claude mcp get akv
```

配置正确就不需要重复添加。只有路径或参数已经过时时，才执行 `claude mcp remove --scope local akv` 删除旧配置，然后重新执行上面的 `add` 命令。

### 11.4 确认连接真的可用

先让 Claude Code 检查 MCP 进程：

```sh
claude mcp get akv
claude mcp list
```

应该能看到 `akv` 配置和 `Status: ✓ Connected`。这一步只能证明 Claude Code 和 stdio MCP 进程可以通信，还不能证明 Agent Token 能访问控制服务。

接着在项目根目录启动 Claude Code：

```sh
claude
```

在 Claude Code 中输入：

```text
请只调用 AKV MCP 的 list_targets，列出可用目标，不要执行任何其他操作。
```

第一次使用 MCP 工具时，Claude Code 可能会询问是否允许调用，确认工具来自 `akv` 后再允许。`list_targets` 返回一个列表就表示 Claude Code、MCP Server、Agent Token 和控制服务已经打通。空列表也算连接成功，只是还没有配置可用目标。

### 11.5 MCP 工具分工

| 用途 | 工具 |
| --- | --- |
| 查看安全的目标信息 | `list_targets`、`get_target` |
| 建立、保活和结束任务 | `begin_task`、`heartbeat_task`、`end_task` |
| 申请、查询和取消授权 | `request_authorization`、`get_authorization_status`、`cancel_authorization_request` |
| 执行获批的一次性操作 | `execute_authorized_operation` |

`begin_task` 成功后，当前 MCP 进程会每 15 秒自动发送心跳，所以演示等待人工批准时不需要手工调用 `heartbeat_task`。退出 Claude Code 只会停止心跳，不会自动调用 `end_task`。如果未结束任务，Worker 会在连续大约 45 秒收不到心跳后把任务标记为失联，并回收未完成授权。

Agent Token 只在 MCP 进程启动时从文件读取一次。在 Web 中轮换 Token 后，需要把新 Token 写入原文件，保持 `0600` 权限，然后退出并重新启动 Claude Code。尽量先结束活动任务再轮换；新 MCP 进程不会恢复旧任务的自动心跳，重启后应调用 `begin_task` 建立新任务。

## 12. 用 Claude Code 跑一次完整样例

这个样例会让 Claude Code 通过 AKV 请求控制服务的 `/healthz`。它会完整经过“发现目标→建立任务→提交申请→人工批准→一次执行→结束任务”。它不会修改目标服务的数据，但会在本地 AKV 和 OpenBao 中创建演示目标、测试凭证、任务、授权和审计记录。

### 12.1 创建演示目标

使用管理员账号打开 `http://127.0.0.1:8080/`，进入“目标与凭证”。如果之前已经按本节创建了名为 `claude-demo` 的启用目标，直接复用，不要再创建。否则点击“新建 HTTP 目标”，按顺序输入：

1. 目标名称：`claude-demo`；
2. 基础 URL：`http://127.0.0.1:8080`；
3. 凭证类型：`API_KEY`；
4. API Key：`local-demo-key-not-secret`。这只是可抛弃的本地测试值，不要使用任何真实 API Key。

AKV 会在请求中注入这个测试 API Key，但 `/healthz` 不使用它，所以任意本地测试值都可以。如果已经有一个 HTTP 测试目标，且确认对它执行 `GET /healthz` 是只读的，可以改用那个目标，并在下面的 Prompt 中替换目标名称。

当前 MVP 表单会让新 HTTP 目标允许申请 `GET`、`POST`、`PUT`、`PATCH` 和 `DELETE`。本次只申请并批准读取 `/healthz` 的 `GET`；批准时仍要核对冻结操作，演示后按第 13 节停用该目标。

### 12.2 让 Claude 提交申请，然后停下等待

回到刚才的 Claude Code 会话，粘贴下面这段 Prompt：

```text
请只通过名为 akv 的 MCP Server 跑一次本地演示，严格按下面顺序执行：
1. 调用 list_targets，找到名称为 claude-demo 的 HTTP 目标；必须只找到一个，找不到或结果不唯一就停止。
2. 调用 begin_task 建立任务。
3. 调用 request_authorization，为该目标申请 HTTP GET /healthz，不带 query 和 body，理由写“验证本地 Claude Code 通过 AKV MCP 的完整授权链路”。
4. 告诉我 task_id 和 request_id，然后立即暂停。
成功得到 request_id 后，在我明确说“已批准”之前，不要调用 execute_authorized_operation，也不要结束任务。如果中途失败且已经建立任务，调用 end_task 并将 outcome 设为 FAILED，然后停止。不要读取、打印或索要任何凭证明文。
```

正常情况下，Claude Code 会依次调用 `list_targets`、`begin_task` 和 `request_authorization`，然后显示 `task_id` 和 `request_id`。这两个 ID 是任务和申请标识，不是凭证。

### 12.3 在 Web 中人工批准

回到 AKV Web 控制台，使用该 Agent 所属账号，或使用具有全局审批权限的管理员账号，进入“待审批”。其他普通账号看不到这个 Agent 的申请。找到刚才的申请，先核对：

- Agent 和 `task_id` 是刚才演示使用的值；
- 目标是 `claude-demo`；
- 冻结的操作是 `GET /healthz`；
- 申请理由与 Prompt 一致。

核对无误后点击“批准”。页面会询问授权必须开始的时限，演示时保持默认的 `10` 分钟即可。如果任何一项不一致，不要批准。

### 12.4 让 Claude 只执行一次并结束任务

回到同一个 Claude Code 会话，输入：

```text
我已在 Web 控制台批准刚才的申请。请继续只使用 AKV MCP：
1. 先用 get_authorization_status 检查刚才的 request_id。
2. 只有状态已批准时，才用刚才的 request_id 和 task_id 调用一次 execute_authorized_operation。
3. 告诉我结果中的 StatusCode。Body 是 Base64 字符串，请说明它解码后的响应内容。不要重试，也不要重新申请授权。
4. 执行后再调用一次 get_authorization_status，告诉我 grant_status、execution_status 和 reclaim_status。
5. 执行成功就调用 end_task 并将 outcome 设为 COMPLETED；状态不是已批准、检查失败或执行失败，就调用 end_task 并将 outcome 设为 FAILED。
```

正常结果中，`StatusCode` 是 `200`，`Body` 是 Base64 字符串。当前 `/healthz` 响应体的 Base64 值是 `eyJzdGF0dXMiOiJvayJ9Cg==`，解码后是 `{"status":"ok"}` 和一个换行符。再次查询申请时，应看到 `grant_status=RECLAIMED`、`execution_status=SUCCEEDED` 和 `reclaim_status=RECLAIMED`。Web 中的申请会保持“已批准”；点击该申请的“审计”，可以查看执行和回收的终态事件。

这次演示中，Claude Code 只知道目标的安全元数据、获批操作和执行结果。测试 API Key 由执行代理从 OpenBao 取得并注入请求，不会经过 Prompt 或 MCP 工具参数。该 Grant 只能原子占用一次；即使执行失败，也不能直接重试，必须重新申请并再次人工批准。

## 13. 如何停止

先在 Claude Code 中让 Claude 调用 `end_task` 结束活动任务，然后退出 Claude Code 会话。由它启动的 MCP Server 会随会话停止。如果会话意外退出而来不及结束任务，Worker 会在连续大约 45 秒收不到心跳后处理失联任务。

再在运行下面进程的终端中分别按 `Ctrl+C`：

- `akv-control`
- `akv-execution-proxy`
- `akv-worker`
- `bao server -dev`

如果以后不再需要这个本地 MCP 配置，可以执行 `claude mcp remove --scope local akv`。

如果不再使用第 12 节的演示目标，可在 Web 的“目标与凭证”中将 `claude-demo` 目标停用。

OpenBao 开发服务器停止后，其中保存的测试凭证可能全部丢失。下次启动时需要重新启用秘密引擎、写入策略并生成运行 Token。

## 14. 常见问题

### 提示配置文件权限不安全

AKV 要求敏感配置文件不能被组用户或其他用户读取。执行：

```sh
chmod 600 /tmp/akv-local/control/database-dsn
chmod 600 /tmp/akv-local/control/openbao-token
chmod 600 /tmp/akv-local/execution/openbao-token
chmod 600 /tmp/akv-local/agent/token
```

### 控制服务启动后立即退出

按顺序检查：

1. PostgreSQL 是否正在运行；
2. DSN 文件内容是否正确；
3. `AKV_CONTROL_LISTEN_ADDRESS` 是否有效且端口未被占用；
4. `AKV_CONTROL_PUBLIC_ORIGIN` 是否为不带路径、query 或 fragment 的 `http` 或 `https` 来源；
5. 控制面 OpenBao Token 文件是否存在、非空且权限为 `0600`。

OpenBao 未启动或秘密引擎未启用不会阻止控制服务监听，但会导致创建目标或更新凭证失败。如果 Web 显示凭证写入失败，再检查 OpenBao 的 `8200` 端口、Token 和相应秘密引擎。

### Claude Code 显示 AKV MCP 连接失败

如果看到 `AKV MCP configuration unavailable`，表示 MCP 进程没有通过启动配置检查。

先执行：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new
claude mcp get akv
```

然后按顺序检查：

1. `bin/akv-mcp-server` 是否存在且可执行；
2. MCP 配置中的命令是否为绝对路径；
3. `/tmp/akv-local/agent/token` 是否为非空普通文件；
4. Token 文件权限是否为 `0600`；
5. `AKV_CONTROL_URL` 和 `AKV_EXECUTION_URL` 是否为带主机的 `http` 或 `https` URL，且不包含用户名密码、query 或 fragment。

如果手工执行 `akv-mcp-server` 后一直没有输出，这是 stdio Server 等待客户端输入的正常行为。请按 `Ctrl+C` 结束手工运行，再让 Claude Code 启动它。

### MCP 工具可见，但调用失败

如果 Claude Code 在调用工具时显示 `AKV_TOOL_FAILED`，说明 stdio 连接已经建立，但后端拒绝或无法完成请求。

如果 `list_targets` 就失败，先检查 `http://127.0.0.1:8080/healthz`、Agent 是否启用、Token 是否过期、撤销或已轮换。轮换 Token 后必须更新 Token 文件并重启 Claude Code，仅覆盖文件不会更新已运行 MCP 进程内存中的 Token。

如果 `list_targets` 成功，但 `execute_authorized_operation` 失败，重点检查：

1. `http://127.0.0.1:8081/healthz` 和执行代理日志；
2. OpenBao 是否还在运行；
3. 申请是否已批准，Grant 是否过期、撤销或已执行；
4. 目标和它的默认凭证是否仍然启用；
5. `request_id` 和 `task_id` 是否属于当前 Agent。

MCP 对后端错误会只显示通用失败信息，不会把可能含敏感细节的响应原文传给 Claude。执行失败时，先调用 `get_authorization_status` 查看 `error_code`，再在 Web 中查看该申请的“审计”。各服务的终端日志可用于确认请求是否到达，但不一定包含具体拒绝原因。

### 前端构建失败

按顺序检查：

1. Node.js 是否为 20.19+ LTS、22.13+ LTS 或 24+；不要使用 Node.js 21、23 等非 LTS 奇数版本；
2. `npm --version` 是否为 10 或更高版本；
3. 当前网络是否可以访问 npm 软件源；
4. 是否在项目根目录执行 `make web-test` 或 `make web-build`；
5. 确认前端源码放在项目根目录的 `web` 中，不要手工修改 `internal/control/web/dist`，它会在每次 Vite 构建时重新生成。

### 执行请求总是被拒绝

确认：

1. Agent Token 没有过期或被轮换；
2. `task_id` 属于当前 Agent，且任务仍为活动状态；
3. 申请已经被人工批准；
4. Grant 没有过期、撤销或执行过；
5. 请求中的目标和操作与审批时冻结的内容完全一致。

### Worker 没有日志

没有需要处理的超时、回收或异常任务时，Worker 不会不断打印日志。只要进程没有退出，通常就是正常运行。

### 端口已经被占用

可以修改下面两个非秘密环境变量：

```text
AKV_CONTROL_LISTEN_ADDRESS
AKV_EXECUTION_LISTEN_ADDRESS
```

修改控制服务端口后，还要同步修改 `AKV_CONTROL_PUBLIC_ORIGIN` 和 MCP Server 使用的 `AKV_CONTROL_URL`。修改执行代理端口后，还要同步修改 MCP Server 使用的 `AKV_EXECUTION_URL`。
