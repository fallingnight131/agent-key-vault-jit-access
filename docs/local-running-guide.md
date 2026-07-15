# AKV 本地运行教程

这篇教程用于在一台开发机上启动 AKV MVP。

完成后，本机会运行：

- PostgreSQL：保存 AKV 的业务数据；
- OpenBao：保存测试凭证；
- `akv-control`：Web 控制台和控制 API，默认端口 `8080`；
- `akv-execution-proxy`：受控执行服务，默认端口 `8081`；
- `akv-worker`：处理超时、回收和异常恢复，不监听端口；
- `akv-mcp-server`：需要让 Agent 使用 AKV 时再启动，通过标准输入输出通信。

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

使用第 6 步创建的管理员账号登录。

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

## 11. 启动 MCP Server

只有需要让 Agent 调用 AKV 时，才需要启动 MCP Server。

MCP Server 使用 stdio 通信，通常应由 Codex、Claude Desktop 或其他 MCP 客户端启动，而不是作为 HTTP 服务单独访问。

它需要下面三个非秘密配置：

```text
AKV_CONTROL_URL=http://127.0.0.1:8080
AKV_EXECUTION_URL=http://127.0.0.1:8081
AKV_AGENT_TOKEN_FILE=/tmp/akv-local/agent/token
```

MCP 客户端的启动命令应指向：

```text
/Users/fallingnight/代码/work/akv-mvp-new/bin/akv-mcp-server
```

启动后，Agent 可以使用以下工具：

- `list_targets`
- `get_target`
- `begin_task`
- `heartbeat_task`
- `end_task`
- `request_authorization`
- `get_authorization_status`
- `execute_authorized_operation`
- `cancel_authorization_request`

MCP Server 会在任务开始后每 15 秒自动发送心跳。MCP Server 退出后心跳停止；Worker 会在失联边界到达后结束任务并回收未完成授权。

## 12. 一次完整的使用过程

服务全部启动后，正常流程如下：

1. 管理员在 Web 控制台添加一个测试目标和测试凭证。
2. Agent 调用 `list_targets`，找到目标 ID。
3. Agent 调用 `begin_task`，得到 `task_id`。
4. Agent 调用 `request_authorization`，提交目标、操作和申请理由。
5. 人类在 Web 控制台查看冻结的操作内容，然后批准或拒绝。
6. Agent 调用 `get_authorization_status` 检查状态。
7. 获批后，Agent 调用 `execute_authorized_operation`。
8. 执行代理原子占用一次性 Grant，然后从 OpenBao 获取凭证并代为执行。
9. 操作结束后系统回收授权。相同授权不能再次执行。
10. Agent 调用 `end_task` 结束任务。

执行失败后也不能直接重试。需要重新提交申请并再次获得人工批准。

## 13. 如何停止

在运行下面进程的终端中分别按 `Ctrl+C`：

- `akv-control`
- `akv-execution-proxy`
- `akv-worker`
- `bao server -dev`

如果 MCP Server 由 MCP 客户端启动，请从客户端停止它。

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
3. OpenBao 是否正在监听 `127.0.0.1:8200`；
4. 控制面 Token 文件是否存在且权限为 `0600`；
5. `kv`、`transit` 和 `database` 引擎是否已经启用。

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
