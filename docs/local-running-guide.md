# AKV 本地运行教程

这篇教程用于在一台开发机上启动 AKV MVP。

完成后，本机会运行：

- PostgreSQL：保存 AKV 的业务数据；
- OpenBao：保存测试凭证；
- `akv-control`：Web 控制台和控制 API，默认端口 `8080`；
- `akv-execution-proxy`：受控执行服务，默认端口 `8081`；
- `akv-worker`：处理超时、回收和异常恢复，不监听端口。

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
akv-bootstrap-admin
```

## 3. 准备本地敏感配置目录

AKV 后端不接受通过命令参数直接传入数据库密码或 OpenBao Token。这些值必须放在只有当前用户可以读取的普通文件中。Agent Token 不属于后端启动配置；本地 MVP 允许第 11 节把它保存到项目根目录 `.agent-token`，但目标源凭证仍然不能保存在项目中。

本教程使用 `/tmp/akv-local`。电脑重启后，这个目录可能被系统清理。

```sh
mkdir -p /tmp/akv-local/control /tmp/akv-local/execution
chmod 700 /tmp/akv-local /tmp/akv-local/control /tmp/akv-local/execution
install -m 600 /dev/null /tmp/akv-local/control/database-dsn
install -m 600 /dev/null /tmp/akv-local/control/openbao-token
install -m 600 /dev/null /tmp/akv-local/execution/openbao-token
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
- 创建可复用的安全操作集，发布操作版本并绑定到目标；
- 审批或拒绝 Agent 的授权申请；
- 查看审计记录和安全告警。

本地使用 HTTP，所以 `AKV_CONTROL_COOKIE_SECURE` 设置为 `false`。正式环境必须使用 HTTPS，并将它设置为 `true`。

## 10. 注册 Agent 并把 Token 交给 Claude Code

在 Web 控制台中注册一个 Agent。当前 Web 会创建有效期为 24 小时的 Token；注册成功后，页面只会显示一次完整 Agent Token。

直连模式下，Agent 运行时必须持有这个 Token，才能以 `Authorization: Bearer <Agent Token>` 调用 AKV。Agent Token 只代表 AKV 中的 Agent 身份，它不是目标系统的 API Key、密码或私钥。

下一节会把 Token 保存到项目根目录 `.agent-token`。这是本地 MVP 唯一允许保存 Agent Token 的项目文件；它已经被 Git 忽略，必须设置为 `0600`，不得提交，也不要把 Token 粘贴到 Claude 对话、请求 JSON、命令行参数、日志或其他文件中。正式环境应使用工作负载身份或专用秘密交付机制。

如果 Token 丢失，请在 Web 控制台中轮换 Token。轮换后，旧 Token 会立即失效，已经启动的 Claude Code 也需要退出并使用新 Token 重新启动。

## 11. 让本地 Claude Code 直接调用 AKV

下面以本地命令行版 Claude Code 为例。Claude Code 会读取项目根目录的 `CLAUDE.md`，并通过项目级 `/akv-access` Skill 的固定客户端处理发现、人工审批、心跳和单次执行；Claude 不再临时拼装 AKV 请求。

### 11.1 检查服务和 Claude Code

先确认控制服务、执行代理和 Worker 都没有退出：

```sh
cd /Users/fallingnight/代码/work/akv-mvp-new

claude --version
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8081/healthz
test -f CLAUDE.md && echo "CLAUDE.md：正常" || echo "CLAUDE.md：缺失"
```

两个 `curl` 都应返回包含 `"status":"ok"` 的 JSON，最后一项应显示“正常”。任何一项失败时都不要继续。如果提示 `claude: command not found`，请先安装 Claude Code。

### 11.2 把 Token 保存到根目录文件并启动 Claude Code

保持 Web 中的 Token 对话框打开。在项目根目录的终端先确认 `.agent-token` 不是符号链接，再创建空文件并限制权限：

```sh
if test -L .agent-token; then
  echo ".agent-token 不能是符号链接，请先手工处理"
else
  touch .agent-token
  chmod 600 .agent-token
  git check-ignore -q .agent-token && echo ".agent-token：已被 Git 忽略"
fi
```

如果看到“`.agent-token 不能是符号链接`”，停止本节，不要粘贴 Token；先手工移走这个符号链接，再重新执行检查。

用本地文本编辑器打开根目录的 `.agent-token`，把 Web 页面显示的完整 Agent Token 粘贴为唯一一行并保存。不要用 `echo <Token>` 之类会把 Token 写入 Shell 历史的命令。保存后执行：

```sh
if chmod 600 .agent-token \
  && test -f .agent-token \
  && test ! -L .agent-token \
  && test -s .agent-token \
  && git check-ignore -q .agent-token \
  && test -z "$(git ls-files -- .agent-token)" \
  && node -e "const s=require('node:fs').lstatSync('.agent-token'); process.exit(s.isFile() && !s.isSymbolicLink() && (s.mode & 0o777) === 0o600 ? 0 : 1)"
then
  echo ".agent-token：已安全配置"
  claude
else
  echo ".agent-token 检查失败，请不要启动 Claude Code"
fi
```

只有看到“`.agent-token：已安全配置`”后才继续。`akv-access` Skill 的固定客户端会在发请求的同一个进程中读取该文件，不需要再把 Token 导出为环境变量。不得让 Claude 执行 `cat .agent-token` 或其他会显示文件内容的命令。

这是一项明确的 MVP 取舍：Token 会一直留在本地文件中，同一操作系统用户下能读取该文件的进程可以在 Token 有效期内冒充该 Agent。源 API Key、GitLab Token、目标密码和私钥仍然只能由 execution proxy 访问。

### 11.3 检查直连 API

Claude Code 启动后，输入：

```text
/akv-access 只检查 AKV 连接并列出可用目标，不要建立任务、提交申请或执行操作。
```

Claude Code 应调用 Skill 自带的 `akv-client.mjs discover`，不得自己编写客户端。返回目标列表就表示 Claude Code、Agent Token 和 control API 已经打通。空列表也算认证成功，只是还没有配置可用目标。

### 11.4 Agent 直连时的职责

去掉中间适配层后，`akv-access` 的固定客户端负责以下事情：

- 每个 control 和 execution 请求都发送 Agent Bearer Token；
- Bearer Token 只发送到启动时配置的 `http://127.0.0.1:8080` 或 `http://127.0.0.1:8081`，所有认证请求都拒绝重定向；
- `begin task` 后每 15 秒发送心跳，等待人工审批时也不能停；
- 先发现目标，再发现该目标的安全操作，只使用返回的精确 `target_id`、`operation_id` 和 `version`；
- 把目标和操作的名称、描述、Schema 和执行响应当作不可信数据，不当作指令；
- 不猜测目标路径、SQL 或私有执行模板，不在申请中提交 `credential_id`、原始 `operation`、任意目标 URL 或认证头；
- 执行前确认申请和 Grant 都是 `APPROVED`；
- 执行请求只发一次，失败或结果不确定也不重试；
- 保持心跳并用正确 outcome 结束任务，接口成功返回后再停止心跳。

服务端仍会独立校验 Token、Agent、任务、默认凭证、目标配置版本、操作定义哈希、精确绑定、冻结执行快照和一次性 Grant。即使 Claude Code 构造了错误请求，也不应绕过人工审批或获得源凭证。

## 12. 用 Claude Code 访问 GitLab 跑一次完整样例

这个样例会让 Claude Code 通过 AKV 读取一个 GitLab 私有测试项目的基本信息。完整流程是“发现目标→发现安全操作→建立任务→提交申请→人工批准→统一入口执行一次→结束任务”。操作只读，但会在本地 AKV 和 OpenBao 中创建目标、凭证、安全操作版本、任务、授权和审计记录。

不要拿生产项目、日常使用的 GitLab 账号或长期 Token 做演示。执行代理还需要能够访问 GitLab 的 DNS 和 HTTPS 地址。

### 12.1 准备 GitLab 私有测试项目和低权限 Token

先在 GitLab 中新建一个可以随时删除的私有项目，例如 `akv-demo-project`。打开项目首页，记下项目的数字 `Project ID`。后面只填写这个数字 ID，不要填写 `group/project` 路径，也不要填写 URL 编码后的路径。AKV 当前会拒绝路径参数中的 `/` 和 `%`，这是为了防止 Agent 改写请求路径。

接着准备一个只用于本次演示的低权限 Token：

- 如果 GitLab 版本支持项目访问令牌，优先创建 Project Access Token。名称可填 `akv-local-demo`，角色选 `Reporter`，Scope 只选 `read_api`，过期时间设为当天或尽量短；
- GitLab.com 的 Project Access Token 需要 Premium 或 Ultimate。如果使用 GitLab.com Free，可新建一个只访问该测试项目的演示账号，再为这个账号创建短期 Personal Access Token，Scope 只选 `read_api`；
- 不要使用 Deploy Token。AKV 的 `ACCESS_TOKEN` 凭证会发送 `Authorization: Bearer <token>`，GitLab 的 Personal、Project 和 Group Access Token 支持这种 REST API 认证方式；
- 如果 GitLab 管理员强制 Personal Access Token 使用 DPoP，本版 AKV 还不会生成 DPoP 证明。此时请改用不要求 DPoP 的专用测试令牌，或换到一个可丢弃的本地 GitLab 测试环境。

GitLab 只会在创建时显示一次 Token。暂时保留创建成功页面，下一节直接把 Token 粘贴进 AKV Web 的密码输入框。不要把它粘贴到 Claude Code 对话、Shell 命令、配置文件或本文档中。

GitLab 官方说明见 [REST API 认证](https://docs.gitlab.com/api/rest/authentication/)、[读取项目 API](https://docs.gitlab.com/api/projects/#get-a-single-project)、[项目访问令牌](https://docs.gitlab.com/user/project/settings/project_access_tokens/) 和 [个人访问令牌](https://docs.gitlab.com/user/profile/personal_access_tokens/)。

### 12.2 在 AKV 创建 GitLab 目标和只读操作

使用管理员账号打开 `http://127.0.0.1:8080/`，进入“目标与凭证”。如果已经有同名测试目标，先确认它确实指向本次测试用的 GitLab 和凭证，不要误用旧配置。点击“新建 HTTP 目标”，按顺序输入：

1. 目标名称：`gitlab-demo`；
2. 基础 URL：GitLab.com 填 `https://gitlab.com`；自建 GitLab 填它最终使用的准确来源，例如 `https://gitlab.example.com`；不要在结尾添加 `/api/v4`；
3. 凭证类型：`ACCESS_TOKEN`；
4. Access Token：粘贴上一节创建的专用测试 Token。

提交后，Control 会把 Token 写入 OpenBao。Web 和 Claude Code 都不能再读取它。真正执行请求时，执行代理才从 OpenBao 取出 Token，在内存中加入 `Authorization: Bearer` 请求头。

执行代理不会跟随 HTTP 重定向，所以自建 GitLab 要直接填写最终来源。当前模板也不适合安装在 `https://example.com/gitlab` 这类相对 URL 子路径中的实例；本地演示请使用 API 位于来源根路径的 GitLab。

在同一页的“安全操作目录”中点击“新建操作集”，输入：

1. 操作集名称：`gitlab-readonly-http`；
2. 操作集说明：`GitLab 只读演示操作`；
3. 执行器类型：`HTTP`。

在这个操作集中点击“发布 v1 操作”，输入：

1. 操作键：`get_project`；
2. 操作名称：`读取 GitLab 项目信息`；
3. 操作说明：`按数字 Project ID 读取一个项目的基本信息`；
4. 风险等级：`LOW`；
5. 公开参数 Schema：

```json
{"type":"object","properties":{"project_id":{"type":"string","description":"GitLab 数字 Project ID","maxLength":20}},"required":["project_id"],"additionalProperties":false}
```

6. 私有执行模板：

```json
{"kind":"HTTP","http":{"method":"GET","path":"/api/v4/projects/{project_id}","path_arguments":{"project_id":"project_id"}}}
```

公开 Schema 会返回给 Agent；私有执行模板只在 Control 中保存和编译，不会通过 Agent API 返回。教程把数字 Project ID 当作字符串提交，例如 `"12345678"`。当前公开 Schema 不能用正则表达式强制它只含数字，所以审批人还要在 Web 中人工核对冻结后的实际路径。

操作发布后，点击 `get_project` 下的“绑定到目标”：

1. 选择 `gitlab-demo` 目标的 ID；
2. 精确版本保持为 `1`；
3. 确认“目标绑定”列表中显示 `gitlab-demo`、`get_project`、`v1` 和“启用”。

只有绑定到目标的精确版本才会被 Agent 发现和申请。以后即使发布 v2，也不会自动把这条绑定从 v1 改到 v2。

### 12.3 让 Claude 提交申请，然后停下等待

把下面 Prompt 中的 `<GITLAB_PROJECT_ID>` 替换成第 12.1 节记下的数字 Project ID，再粘贴到刚才启动的 Claude Code 会话：

```text
/akv-access 读取 gitlab-demo 中数字 Project ID 为 <GITLAB_PROJECT_ID> 的项目基本信息。使用 get_project，申请理由为“读取 GitLab 私有测试项目的基本信息，验证 AKV 一次性授权链路”。提交申请后报告 task_id 和 request_id，保持心跳并等待我审批。
```

正常情况下，Claude Code 会查询目标和公开 Schema、建立任务、启动心跳并提交申请，然后显示 `task_id` 和 `request_id`。这两个 ID 只是任务和申请标识，不是凭证。Claude Code 暂停回答不等于停止后台心跳。

### 12.4 在 Web 中人工批准

回到 AKV Web 控制台，使用该 Agent 所属账号，或使用具有全局审批权限的管理员账号，进入“待审批”。其他普通账号看不到这个 Agent 的申请。找到刚才的申请，核对：

- Agent 和 `task_id` 是本次演示使用的值；
- 目标是 `gitlab-demo`；
- 安全操作是 `get_project` 的精确 `v1`，风险等级是 `LOW`；
- Agent 参数只有数字 Project ID；
- 服务端冻结的实际操作是 `GET /api/v4/projects/<数字 Project ID>`，没有额外 query；
- 申请理由与 Prompt 一致。

全部一致后点击“批准”。页面会询问授权必须开始的时限，演示时保持默认的 `10` 分钟即可。如果任何一项不一致，不要批准。

### 12.5 让 Claude 只执行一次并结束任务

回到同一个 Claude Code 会话，输入：

```text
我已在 Web 控制台处理刚才的申请。请继续按照 akv-access Skill 查询同一个 request_id 的状态；只有申请和 Grant 都已批准且 Grant 未过期时才使用固定客户端执行一次。报告 GitLab 状态码、id、name、path_with_namespace、visibility、web_url，以及最终回收状态。
```

正确配置时，GitLab 应返回 `StatusCode=200`，解码后的 JSON 会包含这个测试项目的信息。响应内容和 Base64 值会随项目变化，所以不要照抄固定结果。再次查询申请时，应看到 `grant_status=RECLAIMED`、`execution_status=SUCCEEDED` 和 `reclaim_status=RECLAIMED`。Web 中的申请会保持“已批准”；点击该申请的“审计”，可以查看执行和回收的终态事件。

注意：`execution_status=SUCCEEDED` 表示 AKV 已经把 HTTP 请求送到 GitLab 并收到响应，不代表 GitLab 的业务响应一定成功。即使 GitLab 返回 `401`、`403` 或 `404`，该 Grant 也已经被使用并回收，不能重试；修正配置后必须重新建立任务、重新申请并再次人工批准。

这次演示中，Claude Code 只会得到目标的安全元数据、操作的公开 Schema 和精确版本，不能读取私有模板或 GitLab Token，也不能把已批准的请求改成其他 URL。Control 在服务端生成并冻结实际操作，审批人可以在 Web 中核对。GitLab Token 只由执行代理从 OpenBao 取出并注入请求。Grant 只能原子占用一次，无论成功还是失败都不能重复执行。

### 12.6 演示后立即撤销 Token

演示结束后，先回到 GitLab 撤销刚才创建的 Project Access Token 或 Personal Access Token。然后在 AKV Web 的“目标与凭证”中停用 `gitlab-demo` 目标或它的默认凭证。

只在 AKV 中停用目标，并不会替你撤销 GitLab 上游的 Token，也不会删除 OpenBao 中保存的静态源凭证。因此，GitLab 侧撤销是必须做的最后一步。这个演示 Token 不要再次使用。

## 13. 如何停止

先让 Claude Code 在保持心跳的情况下通过任务结束 API 结束活动任务；接口成功返回 204 后，再停止后台心跳并退出 Claude Code 会话。如果会话意外退出而来不及结束任务，Worker 会在连续大约 45 秒收不到心跳后处理失联任务。

退出 Claude Code 后不需要清理环境变量。根目录 `.agent-token` 会保留，方便下次本地演示继续使用。如果以后不再使用这个 Agent，应先在 Web 中撤销它的 Token，再删除本地 `.agent-token`；如果在 Web 中轮换 Token，则用新值覆盖该文件。

再在运行下面进程的终端中分别按 `Ctrl+C`：

- `akv-control`
- `akv-execution-proxy`
- `akv-worker`
- `bao server -dev`

如果使用了第 12 节的 GitLab 演示，请确认已经完成第 12.6 节的上游 Token 撤销和 `gitlab-demo` 目标停用。

OpenBao 开发服务器停止后，其中保存的测试凭证可能全部丢失。下次启动时需要重新启用秘密引擎、写入策略并生成运行 Token。

## 14. 常见问题

### 提示配置文件权限不安全

AKV 要求敏感配置文件不能被组用户或其他用户读取。执行：

```sh
chmod 600 /tmp/akv-local/control/database-dsn
chmod 600 /tmp/akv-local/control/openbao-token
chmod 600 /tmp/akv-local/execution/openbao-token
```

### 控制服务启动后立即退出

按顺序检查：

1. PostgreSQL 是否正在运行；
2. DSN 文件内容是否正确；
3. `AKV_CONTROL_LISTEN_ADDRESS` 是否有效且端口未被占用；
4. `AKV_CONTROL_PUBLIC_ORIGIN` 是否为不带路径、query 或 fragment 的 `http` 或 `https` 来源；
5. 控制面 OpenBao Token 文件是否存在、非空且权限为 `0600`。

OpenBao 未启动或秘密引擎未启用不会阻止控制服务监听，但会导致创建目标或更新凭证失败。如果 Web 显示凭证写入失败，再检查 OpenBao 的 `8200` 端口、Token 和相应秘密引擎。

### Claude Code 提示 Agent Token 文件未配置

退出 Claude Code，确认当前目录是项目根目录，并按第 11.2 节检查 `.agent-token` 存在、非空、已被 Git 忽略且权限为 `0600`。不要把 Token 粘贴到对话中，也不要用 `cat`、`head` 或其他命令检查文件内容。

如果刚在 Web 中轮换了 Token，旧 Token 会立即失效。用新 Token 覆盖 `.agent-token`，然后重新启动 Claude Code。

### 直连 API 返回 401 或调用失败

如果查询目标就返回 `401 UNAUTHORIZED`，请确认 Agent 仍然启用，Token 没有过期、撤销或被轮换，并确认 Claude Code 从包含当前 `.agent-token` 的项目根目录启动。

如果查询目标成功，但执行 API 失败，重点检查：

1. `http://127.0.0.1:8081/healthz` 和执行代理日志；
2. OpenBao 是否还在运行；
3. 申请是否已批准，Grant 是否过期、撤销或已执行；
4. 目标和它的默认凭证是否仍然启用；
5. 申请后操作/操作集或目标绑定是否被停用，绑定是否换了版本，目标连接配置是否发生变化；这些变化都会让旧 Grant 默认拒绝；
6. `request_id` 和 `task_id` 是否属于当前 Agent；
7. Claude Code 是否把发现和授权申请发送到了 `8080`，把统一执行请求发送到了 `8081`。

后端只返回收敛后的错误码，不会返回源凭证或 OpenBao 错误原文。执行失败时，调用申请状态 API 查看 `error_code`，再在 Web 中查看该申请的“审计”。各服务的终端日志可用于确认请求是否到达，但不一定包含具体拒绝原因。

### GitLab 返回 401、403 或 404

先看执行响应中的 `result.StatusCode`，再按顺序检查：

1. `gitlab-demo` 的基础 URL 是否为 GitLab 最终使用的来源，例如 `https://gitlab.com`，没有重复添加 `/api/v4`，也不会发生重定向；
2. 参数是否为数字 Project ID 的字符串，没有使用 `group/project`；
3. Token 是否已过期、被撤销或粘贴不完整，是否至少具有 `read_api` Scope；
4. Token 所属用户或项目令牌是否能够访问这个私有测试项目；GitLab 对无权查看的私有项目可能返回 `404`；
5. GitLab 是否强制 Personal Access Token 使用 DPoP；本版 AKV 不能生成 DPoP 证明；
6. 执行代理所在机器是否能解析 GitLab 域名并访问它的 HTTPS 端口；自建 GitLab 使用私有 CA 时，系统信任库是否已经信任该 CA。

通常，`401` 表示 Token 无效、过期或缺少 GitLab 要求的认证证明，`403` 表示权限或实例策略拒绝，`404` 表示 Project ID 错误或该 Token 看不到私有项目。修正后不要重发原执行请求，因为原 Grant 已经被占用；请重新建立任务、提交申请并审批。

### GitLab 返回 502 Bad Gateway

如果执行响应是 AKV HTTP `200`、`result.StatusCode=502`、`execution_status=SUCCEEDED` 和 `grant_status=RECLAIMED`，说明 Grant 在有效期内被正常占用，AKV 也完成了唯一一次 HTTP 交换；失败发生在授权之后的目标网络链路。这个 502 可能由中间网关或 GitLab 返回，不能仅凭状态码断定来源。`execution_status=SUCCEEDED` 只表示收到了 HTTP 响应，GitLab 的业务结果仍然是失败。

先在运行 execution proxy 的同一台机器上做不带任何 Token 的探测：

```sh
curl -sS --max-time 10 -o /dev/null \
  -w 'default code=%{http_code} remote=%{remote_ip}\n' \
  http://git.koal.com/api/v4/projects/12747

curl -sS --noproxy '*' --max-time 10 -o /dev/null \
  -w 'direct code=%{http_code} remote=%{remote_ip}\n' \
  http://git.koal.com/api/v4/projects/12747
```

不要在诊断命令中加入 GitLab Token。如果默认路径连接到 `127.0.0.1` 并返回 `502`，而直连提示无法解析域名，说明 execution proxy 继承的 `HTTP_PROXY`/`HTTPS_PROXY` 把请求交给了本机代理，但该代理也无法解析或连接内部 GitLab。按实际网络方式处理：

1. 需要直连内部 GitLab：先连接公司网络或 VPN，确保系统 DNS 能解析 `git.koal.com`，再把该域名加入 execution proxy 进程的 `NO_PROXY` 并重启 execution proxy；
2. 必须经过代理：修复代理对 `git.koal.com` 的 DNS 和路由，不要简单绕过；
3. 把 GitLab Base URL 改成它最终可达的准确 HTTPS Origin。明文 HTTP 无论直连还是经代理都可能暴露 GitLab Token，不适合正式环境。

修好网络后必须创建新任务、重新申请并审批；502 对应的旧 Grant 已经消费并回收，不能重试。

### 等待审批时任务变成 AGENT_LOST

Claude Code 建立任务后必须每 15 秒发送一次任务心跳，等待人工审批时也不能停。查询申请状态不能代替心跳。连续大约 45 秒没有心跳时，Worker 会结束失联任务并撤销未完成的授权；此时应建立新任务、重新申请并重新等待人工审批。

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
5. 该目标仍然绑定同一 `operation_id` 和精确版本，且操作集、操作、绑定、目标和默认凭证都仍然启用；
6. 目标连接配置没有在申请后变化。如果已变化，请重新发现操作、创建申请并重新审批。

### Worker 没有日志

没有需要处理的超时、回收或异常任务时，Worker 不会不断打印日志。只要进程没有退出，通常就是正常运行。

### 端口已经被占用

可以修改下面两个非秘密环境变量：

```text
AKV_CONTROL_LISTEN_ADDRESS
AKV_EXECUTION_LISTEN_ADDRESS
```

修改控制服务端口后，还要同步修改 `AKV_CONTROL_PUBLIC_ORIGIN` 和启动 Claude Code 时设置的 `AKV_CONTROL_URL`。修改执行代理端口后，还要同步修改 Claude Code 使用的 `AKV_EXECUTION_URL`。
