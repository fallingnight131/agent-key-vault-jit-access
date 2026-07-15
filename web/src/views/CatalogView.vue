<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { encodeBase64 } from '../helpers.js'
import ModalDialog from '../components/ModalDialog.vue'

const props = defineProps({ api: { type: Function, required: true } })
const targets = ref([])
const credentials = ref([])
const operationSets = ref([])
const operations = ref([])
const operationVersions = ref([])
const operationBindings = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const dialogKind = ref('')
const dialogContext = ref(null)
const dialogData = ref({})
const dialogError = ref('')

const emptyArgumentsSchema = {
  type: 'object',
  properties: {},
  required: [],
  additionalProperties: false,
}

const dialogConfig = computed(() => {
  if (dialogKind.value === 'http-target') {
    return {
      title: '新建 HTTP 目标',
      description: '配置目标地址和默认凭证。凭证明文只写入一次，提交后无法从控制台读取。',
      submitLabel: '创建目标',
      wide: true,
    }
  }
  if (dialogKind.value === 'postgres-target') {
    return {
      title: '新建 PostgreSQL 目标',
      description: '选择固定凭证或 OpenBao 动态凭证，并填写数据库连接信息。',
      submitLabel: '创建目标',
      wide: true,
    }
  }
  if (dialogKind.value === 'credential') {
    const credential = dialogContext.value?.credential
    return {
      title: '更新凭证',
      description: `${credential?.alias || '默认凭证'} · ${credential?.credential_type || ''}。新值提交后不会再次显示。`,
      submitLabel: '更新凭证',
      wide: true,
    }
  }
  if (dialogKind.value === 'operation-set') {
    return {
      title: '新建安全操作集',
      description: '同一执行器类型的安全操作可以复用并绑定到多个兼容目标。',
      submitLabel: '创建操作集',
    }
  }
  if (dialogKind.value === 'operation') {
    return {
      title: dialogContext.value?.operation ? '发布操作新版本' : '发布 v1 操作',
      description: '公开 Schema 会返回给 Agent；私有执行模板只供 Control 编译和审批展示。',
      submitLabel: dialogContext.value?.operation ? '发布新版本' : '发布 v1',
      wide: true,
    }
  }
  return {
    title: dialogContext.value?.binding ? '升级目标绑定' : '绑定操作到目标',
    description: '绑定会固定目标、操作和精确版本；以后发布新版本不会自动升级。',
    submitLabel: dialogContext.value?.binding ? '确认升级' : '创建绑定',
  }
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await props.api('/v1/web/catalog')
    targets.value = data.targets || []
    credentials.value = data.credentials || []
    operationSets.value = data.operation_sets || []
    operations.value = data.operations || []
    operationVersions.value = data.operation_versions || []
    operationBindings.value = data.operation_bindings || []
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

function credentialDefaults(type = 'API_KEY') {
  return {
    credential_type: type,
    api_key: '',
    access_token: '',
    username: '',
    password: '',
    certificate: '',
    private_key: '',
    transit_key_type: 'ecdsa-p256',
    connection_name: '',
    creation_statement: '',
  }
}

function takeEncoded(data, key) {
  const value = data[key]
  data[key] = ''
  return encodeBase64(value)
}

function credentialMaterial(type, data) {
  if (type === 'API_KEY' && data.api_key) {
    return { secret_values: { api_key: takeEncoded(data, 'api_key') } }
  }
  if (type === 'ACCESS_TOKEN' && data.access_token) {
    return { secret_values: { access_token: takeEncoded(data, 'access_token') } }
  }
  if (type === 'USERNAME_PASSWORD' && data.username && data.password) {
    return {
      secret_values: {
        username: takeEncoded(data, 'username'),
        password: takeEncoded(data, 'password'),
      },
    }
  }
  if (type === 'CERTIFICATE' && data.certificate && data.private_key) {
    return {
      secret_values: {
        certificate: takeEncoded(data, 'certificate'),
        private_key: takeEncoded(data, 'private_key'),
      },
    }
  }
  if (type === 'TRANSIT_KEY' && data.transit_key_type) {
    return { transit_key_type: data.transit_key_type.trim() }
  }
  if (type === 'POSTGRESQL_DYNAMIC' && data.connection_name && data.creation_statement) {
    const creationStatement = data.creation_statement
    data.creation_statement = ''
    return {
      database_role: {
        connection_name: data.connection_name.trim(),
        creation_statements: [creationStatement],
        default_ttl: 60000000000,
        max_ttl: 300000000000,
      },
    }
  }
  dialogError.value = '请填写当前凭证类型要求的全部字段'
  return null
}

function createHTTPTarget() {
  openDialog('http-target', {
    name: '',
    base_url: '',
    ...credentialDefaults('API_KEY'),
  })
}

function createPostgreSQLTarget() {
  openDialog('postgres-target', {
    name: '',
    host: '',
    port: 5432,
    database: '',
    ...credentialDefaults('USERNAME_PASSWORD'),
  })
}

async function toggleTarget(target) {
  await mutate(() => props.api(`/v1/web/targets/${target.id}`, {
    method: 'PATCH',
    body: JSON.stringify({
      description: target.description,
      connection_config: target.connection_config,
      active: !target.active,
    }),
  }))
}

async function toggleCredential(credential) {
  await mutate(() => props.api(`/v1/web/credentials/${credential.id}`, {
    method: 'PATCH',
    body: JSON.stringify({ active: !credential.active }),
  }))
}

function rotateCredential(credential) {
  openDialog('credential', credentialDefaults(credential.credential_type), { credential })
}

function operationsFor(setID) {
  return operations.value.filter((operation) => operation.operation_set_id === setID)
}

function versionsFor(operationID) {
  return operationVersions.value
    .filter((version) => version.operation_id === operationID)
    .sort((left, right) => left.version - right.version)
}

function currentVersionFor(operation) {
  return operationVersions.value.find((version) => (
    version.operation_id === operation.id && version.version === operation.current_version
  ))
}

function operationFor(operationID) {
  return operations.value.find((operation) => operation.id === operationID)
}

function targetName(targetID) {
  const target = targets.value.find((candidate) => candidate.id === targetID)
  return target ? `${target.name} (${target.id})` : targetID
}

function operationName(operationID) {
  const operation = operationFor(operationID)
  if (!operation) return operationID
  const version = currentVersionFor(operation)
  return `${version?.name || operation.key} (${operation.key})`
}

function prettyJSON(value) {
  try {
    const parsed = typeof value === 'string' ? JSON.parse(value) : value
    return JSON.stringify(parsed, null, 2)
  } catch {
    return '无法显示的 JSON'
  }
}

function defaultTemplate(executorType) {
  if (executorType === 'POSTGRESQL') {
    return {
      kind: 'POSTGRESQL_STATEMENT',
      postgresql: { statements: [{ sql: 'SELECT 1' }] },
    }
  }
  if (executorType === 'SIGN') {
    return {
      kind: 'SIGN',
      sign: { algorithm: 'sha2-256', digest_argument: 'digest' },
    }
  }
  return { kind: 'HTTP', http: { method: 'GET', path: '/' } }
}

function defaultSchema(executorType) {
  if (executorType !== 'SIGN') return emptyArgumentsSchema
  return {
    type: 'object',
    properties: { digest: { type: 'string' } },
    required: ['digest'],
    additionalProperties: false,
  }
}

function parseJSONObject(label, text) {
  try {
    const parsed = JSON.parse(text)
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('not an object')
    return parsed
  } catch {
    dialogError.value = `${label}必须是有效的 JSON 对象`
    return null
  }
}

function openOperationDefinition(set, operation = null) {
  const current = operation ? currentVersionFor(operation) : null
  openDialog('operation', {
    key: '',
    name: current?.name || '',
    description: current?.description || '',
    risk_level: current?.risk_level || 'LOW',
    arguments_schema: prettyJSON(current?.arguments_schema || defaultSchema(set.executor_type)),
    execution_template: prettyJSON(current?.execution_template || defaultTemplate(set.executor_type)),
  }, { set, operation })
}

function createOperationSet() {
  openDialog('operation-set', { name: '', description: '', executor_type: 'HTTP' })
}

function createOperation(set) {
  openOperationDefinition(set)
}

function publishOperationVersion(set, operation) {
  openOperationDefinition(set, operation)
}

async function toggleOperationSet(set) {
  await mutate(() => props.api(`/v1/web/operation-sets/${set.id}`, {
    method: 'PATCH',
    body: JSON.stringify({ active: !set.active }),
  }))
}

async function toggleOperation(operation) {
  await mutate(() => props.api(`/v1/web/operations/${operation.id}`, {
    method: 'PATCH',
    body: JSON.stringify({ active: !operation.active }),
  }))
}

function bindOperation(operation) {
  error.value = ''
  if (!targets.value.length) {
    error.value = '请先创建可绑定的目标'
    return
  }
  openDialog('binding', {
    target_id: targets.value[0].id,
    version: operation.current_version,
  }, { operation })
}

function upgradeBinding(binding) {
  error.value = ''
  const operation = operationFor(binding.operation_id)
  if (!operation) {
    error.value = '找不到这条绑定对应的操作'
    return
  }
  openDialog('binding', {
    target_id: binding.target_id,
    version: binding.version,
  }, { operation, binding })
}

async function toggleBinding(binding) {
  await saveBinding(binding.target_id, binding.operation_id, binding.version, !binding.active)
}

async function saveBinding(targetID, operationID, version, active) {
  await mutate(() => props.api(`/v1/web/targets/${targetID}/operations/${operationID}`, {
    method: 'PUT',
    body: JSON.stringify({ version, active }),
  }))
}

function openDialog(kind, data, context = null) {
  wipeDialogSecrets()
  error.value = ''
  dialogError.value = ''
  dialogKind.value = kind
  dialogData.value = { ...data }
  dialogContext.value = context
}

function closeDialog(force = false) {
  if (busy.value && !force) return
  wipeDialogSecrets()
  dialogKind.value = ''
  dialogData.value = {}
  dialogContext.value = null
  dialogError.value = ''
}

function wipeDialogSecrets() {
  const data = dialogData.value
  for (const key of ['api_key', 'access_token', 'username', 'password', 'certificate', 'private_key', 'creation_statement']) {
    if (typeof data[key] === 'string') data[key] = ''
  }
}

async function submitDialog() {
  const data = dialogData.value
  let path
  let method = 'POST'
  let payload

  if (dialogKind.value === 'http-target') {
    let baseURL
    try {
      baseURL = new URL(data.base_url?.trim() || '')
    } catch {
      baseURL = null
    }
    if (!data.name?.trim() || !baseURL || !['http:', 'https:'].includes(baseURL.protocol) || baseURL.username || baseURL.password) {
      dialogError.value = '请填写目标名称和不含账号信息的 HTTP(S) 基础 URL'
      return
    }
    const material = credentialMaterial(data.credential_type, data)
    if (!material) return
    path = '/v1/web/targets'
    payload = {
      name: data.name.trim(),
      description: '',
      connector_type: 'HTTP',
      connection_config: {
        base_url: baseURL.href.replace(/\/$/, ''),
        allowed_http_methods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'],
      },
      credential_alias: 'default',
      credential_type: data.credential_type,
      ...material,
    }
  } else if (dialogKind.value === 'postgres-target') {
    const port = Number(data.port)
    if (!data.name?.trim() || !data.host?.trim() || !data.database?.trim() || !Number.isInteger(port) || port < 1 || port > 65535) {
      dialogError.value = '请填写目标名称、主机、数据库和 1–65535 的端口'
      return
    }
    const material = credentialMaterial(data.credential_type, data)
    if (!material) return
    const dynamic = data.credential_type === 'POSTGRESQL_DYNAMIC'
    path = '/v1/web/targets'
    payload = {
      name: data.name.trim(),
      description: '',
      connector_type: 'POSTGRESQL',
      connection_config: {
        host: data.host.trim(),
        port,
        database: data.database.trim(),
        tls_mode: 'require',
        require_dynamic: dynamic,
      },
      credential_alias: 'default',
      credential_type: data.credential_type,
      ...material,
    }
  } else if (dialogKind.value === 'credential') {
    const credential = dialogContext.value?.credential
    const material = credentialMaterial(credential?.credential_type, data)
    if (!credential || !material) return
    path = `/v1/web/credentials/${credential.id}`
    method = 'PATCH'
    payload = material
  } else if (dialogKind.value === 'operation-set') {
    if (!data.name?.trim() || !['HTTP', 'POSTGRESQL', 'SIGN'].includes(data.executor_type)) {
      dialogError.value = '请填写操作集名称并选择执行器类型'
      return
    }
    path = '/v1/web/operation-sets'
    payload = {
      name: data.name.trim(),
      description: data.description?.trim() || '',
      executor_type: data.executor_type,
    }
  } else if (dialogKind.value === 'operation') {
    const { set, operation } = dialogContext.value || {}
    if (!set || (!operation && !/^[a-z0-9_]+$/.test(data.key?.trim() || '')) || !data.name?.trim()) {
      dialogError.value = '请填写名称；新操作键只能包含小写字母、数字和下划线'
      return
    }
    if (!['LOW', 'MEDIUM', 'HIGH'].includes(data.risk_level)) {
      dialogError.value = '请选择正确的风险等级'
      return
    }
    const argumentsSchema = parseJSONObject('公开参数 Schema', data.arguments_schema)
    if (!argumentsSchema) return
    const executionTemplate = parseJSONObject('私有执行模板', data.execution_template)
    if (!executionTemplate) return
    path = operation
      ? `/v1/web/operations/${operation.id}/versions`
      : `/v1/web/operation-sets/${set.id}/operations`
    payload = {
      ...(!operation ? { key: data.key.trim() } : {}),
      name: data.name.trim(),
      description: data.description?.trim() || '',
      risk_level: data.risk_level,
      arguments_schema: argumentsSchema,
      execution_template: executionTemplate,
    }
  } else if (dialogKind.value === 'binding') {
    const { operation, binding } = dialogContext.value || {}
    const version = Number(data.version)
    const available = operation ? versionsFor(operation.id).map((item) => item.version) : []
    if (!operation || !targets.value.some((target) => target.id === data.target_id) || !available.includes(version)) {
      dialogError.value = '请选择有效的目标和已发布版本'
      return
    }
    path = `/v1/web/targets/${data.target_id}/operations/${operation.id}`
    method = 'PUT'
    payload = { version, active: binding?.active ?? true }
  } else {
    return
  }

  busy.value = true
  dialogError.value = ''
  try {
    await props.api(path, { method, body: JSON.stringify(payload) })
    closeDialog(true)
    await load()
  } catch (failure) {
    dialogError.value = failure.message
  } finally {
    wipeDialogSecrets()
    busy.value = false
  }
}

async function mutate(operation) {
  busy.value = true
  error.value = ''
  try {
    await operation()
    await load()
    return true
  } catch (failure) {
    error.value = failure.message
    return false
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => closeDialog(true))
</script>

<template>
  <div class="toolbar">
    <p class="muted">凭证明文只能写入，控制台不提供查看或导出。</p>
    <div class="actions compact">
      <button type="button" :disabled="busy" @click="createHTTPTarget">新建 HTTP 目标</button>
      <button type="button" class="secondary" :disabled="busy" @click="createPostgreSQLTarget">新建 PostgreSQL 目标</button>
    </div>
  </div>
  <p v-if="error" class="error" role="alert">{{ error }}</p>
  <p v-if="loading" class="muted">正在加载…</p>
  <template v-else>
    <h2 class="section-title catalog-first-title">目标系统</h2>
    <div v-if="targets.length" class="grid cards">
      <article v-for="target in targets" :key="target.id" class="card">
        <span class="badge">{{ target.active ? '启用' : '停用' }}</span>
        <h2>{{ target.name }}</h2>
        <p class="muted">{{ target.connector_type }} · {{ target.description || '无描述' }}</p>
        <button type="button" class="secondary" :disabled="busy" @click="toggleTarget(target)">{{ target.active ? '停用' : '启用' }}</button>
      </article>
    </div>
    <div v-else class="empty">暂无目标</div>

    <section class="operation-catalog" aria-labelledby="operation-catalog-title">
      <div class="toolbar operation-toolbar">
        <div>
          <h2 id="operation-catalog-title" class="section-title">安全操作目录</h2>
          <p class="muted">只有绑定到目标的精确版本才能被 Agent 申请；发布新版本不会自动升级已有绑定。</p>
        </div>
        <button type="button" :disabled="busy" @click="createOperationSet">新建操作集</button>
      </div>

      <div v-if="operationSets.length" class="grid operation-sets">
        <article v-for="set in operationSets" :key="set.id" class="card operation-set">
          <div class="operation-heading">
            <div>
              <span class="badge">{{ set.active ? '启用' : '停用' }}</span>
              <h3>{{ set.name }}</h3>
              <p class="muted">{{ set.executor_type }} · {{ set.description || '无说明' }}</p>
            </div>
            <div class="actions compact">
              <button type="button" :disabled="busy" @click="createOperation(set)">发布 v1 操作</button>
              <button type="button" class="secondary" :disabled="busy" @click="toggleOperationSet(set)">{{ set.active ? '停用操作集' : '启用操作集' }}</button>
            </div>
          </div>

          <div v-if="operationsFor(set.id).length" class="grid operation-list">
            <section v-for="item in operationsFor(set.id)" :key="item.id" class="operation-item">
              <div class="operation-heading">
                <div>
                  <span class="badge">{{ item.active ? '启用' : '停用' }}</span>
                  <h4>{{ currentVersionFor(item)?.name || item.key }}</h4>
                  <p class="muted"><code>{{ item.key }}</code> · 当前 v{{ item.current_version }}</p>
                </div>
                <span class="badge risk">{{ currentVersionFor(item)?.risk_level || '未知风险' }}</span>
              </div>
              <p>{{ currentVersionFor(item)?.description || '无说明' }}</p>
              <p class="schema-label">公开参数 Schema</p>
              <pre class="operation operation-schema">{{ prettyJSON(currentVersionFor(item)?.arguments_schema || {}) }}</pre>
              <p class="muted version-list">已发布：{{ versionsFor(item.id).map((version) => `v${version.version}`).join('、') || '无' }}</p>
              <div class="actions">
                <button type="button" :disabled="busy" @click="publishOperationVersion(set, item)">发布新版本</button>
                <button type="button" class="secondary" :disabled="busy" @click="bindOperation(item)">绑定到目标</button>
                <button type="button" class="secondary" :disabled="busy" @click="toggleOperation(item)">{{ item.active ? '停用操作' : '启用操作' }}</button>
              </div>
            </section>
          </div>
          <div v-else class="empty compact-empty">这个操作集还没有已发布操作</div>
        </article>
      </div>
      <div v-else class="empty">暂无安全操作集</div>

      <h3 class="section-title">目标绑定</h3>
      <div v-if="operationBindings.length" class="table-wrap">
        <table>
          <thead><tr><th>目标</th><th>操作</th><th>精确版本</th><th>状态</th><th>管理</th></tr></thead>
          <tbody>
            <tr v-for="binding in operationBindings" :key="`${binding.target_id}:${binding.operation_id}`">
              <td>{{ targetName(binding.target_id) }}</td>
              <td>{{ operationName(binding.operation_id) }}</td>
              <td><strong>v{{ binding.version }}</strong></td>
              <td><span class="badge">{{ binding.active ? '启用' : '停用' }}</span></td>
              <td>
                <div class="actions compact">
                  <button type="button" class="secondary" :disabled="busy" @click="upgradeBinding(binding)">升级版本</button>
                  <button type="button" class="secondary" :disabled="busy" @click="toggleBinding(binding)">{{ binding.active ? '停用绑定' : '启用绑定' }}</button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <div v-else class="empty">暂无目标操作绑定</div>
    </section>

    <h2 class="section-title">凭证元数据</h2>
    <div v-if="credentials.length" class="grid cards">
      <article v-for="credential in credentials" :key="credential.id" class="card">
        <span class="badge">{{ credential.active ? '启用' : '停用' }}</span>
        <h2>{{ credential.alias }}</h2>
        <p class="muted">{{ credential.credential_type }}</p>
        <div class="actions">
          <button type="button" class="secondary" :disabled="busy" @click="rotateCredential(credential)">更新凭证</button>
          <button type="button" class="secondary" :disabled="busy" @click="toggleCredential(credential)">{{ credential.active ? '停用' : '启用' }}</button>
        </div>
      </article>
    </div>
    <div v-else class="empty">暂无凭证</div>
  </template>

  <ModalDialog
    :open="Boolean(dialogKind)"
    :title="dialogConfig.title"
    :description="dialogConfig.description"
    :submit-label="dialogConfig.submitLabel"
    :wide="dialogConfig.wide"
    :busy="busy"
    :error="dialogError"
    @close="closeDialog"
    @submit="submitDialog"
  >
    <div v-if="dialogKind === 'http-target'" class="modal-grid">
      <label>
        目标名称
        <input v-model="dialogData.name" name="target_name" maxlength="128" required autofocus autocomplete="off">
      </label>
      <label>
        基础 URL
        <input v-model="dialogData.base_url" name="base_url" type="url" placeholder="https://gitlab.com" required autocomplete="off">
      </label>
      <label class="span-2">
        凭证类型
        <select v-model="dialogData.credential_type" name="credential_type" required @change="wipeDialogSecrets">
          <option value="API_KEY">API Key</option>
          <option value="ACCESS_TOKEN">Access Token</option>
          <option value="USERNAME_PASSWORD">用户名和密码</option>
          <option value="CERTIFICATE">证书和私钥（仅存储）</option>
          <option value="TRANSIT_KEY">OpenBao Transit Key</option>
        </select>
      </label>
    </div>

    <div v-else-if="dialogKind === 'postgres-target'" class="modal-grid">
      <label>
        目标名称
        <input v-model="dialogData.name" name="target_name" maxlength="128" required autofocus autocomplete="off">
      </label>
      <label>
        主机名或 IP
        <input v-model="dialogData.host" name="database_host" required autocomplete="off">
      </label>
      <label>
        端口
        <input v-model.number="dialogData.port" name="database_port" type="number" min="1" max="65535" step="1" required>
      </label>
      <label>
        数据库名
        <input v-model="dialogData.database" name="database_name" required autocomplete="off">
      </label>
      <label class="span-2">
        凭证方式
        <select v-model="dialogData.credential_type" name="credential_type" required @change="wipeDialogSecrets">
          <option value="USERNAME_PASSWORD">固定用户名和密码</option>
          <option value="POSTGRESQL_DYNAMIC">OpenBao 动态凭证</option>
        </select>
      </label>
    </div>

    <div v-if="['http-target', 'postgres-target', 'credential'].includes(dialogKind)" class="modal-grid">
      <label v-if="dialogData.credential_type === 'API_KEY'" class="span-2">
        API Key（只写入一次）
        <input v-model="dialogData.api_key" name="api_key" type="password" required autocomplete="off" spellcheck="false">
      </label>
      <label v-if="dialogData.credential_type === 'ACCESS_TOKEN'" class="span-2">
        Access Token（只写入一次）
        <input v-model="dialogData.access_token" name="access_token" type="password" required autocomplete="off" spellcheck="false">
      </label>
      <template v-if="dialogData.credential_type === 'USERNAME_PASSWORD'">
        <label>
          目标用户名
          <input v-model="dialogData.username" name="credential_username" required autocomplete="off" spellcheck="false">
        </label>
        <label>
          目标密码（只写入一次）
          <input v-model="dialogData.password" name="credential_password" type="password" required autocomplete="off" spellcheck="false">
        </label>
      </template>
      <template v-if="dialogData.credential_type === 'CERTIFICATE'">
        <label class="span-2">
          PEM 证书（仅存储）
          <textarea v-model="dialogData.certificate" name="certificate" class="code-input" required autocomplete="off" spellcheck="false"></textarea>
        </label>
        <label class="span-2">
          PEM 私钥（只写入一次）
          <textarea v-model="dialogData.private_key" name="private_key" class="code-input" required autocomplete="off" spellcheck="false"></textarea>
        </label>
      </template>
      <label v-if="dialogData.credential_type === 'TRANSIT_KEY'" class="span-2">
        Transit Key 类型
        <input v-model="dialogData.transit_key_type" name="transit_key_type" required autocomplete="off" spellcheck="false">
      </label>
      <template v-if="dialogData.credential_type === 'POSTGRESQL_DYNAMIC'">
        <label class="span-2">
          OpenBao Database Connection 名称
          <input v-model="dialogData.connection_name" name="connection_name" required autocomplete="off" spellcheck="false">
        </label>
        <label class="span-2">
          创建临时用户的 SQL
          <textarea v-model="dialogData.creation_statement" name="creation_statement" class="code-input" required autocomplete="off" spellcheck="false"></textarea>
        </label>
      </template>
      <p class="field-help span-2">敏感字段只会在本次提交期间保存在页面内存中，提交、取消或离开页面后会清空。</p>
    </div>

    <div v-else-if="dialogKind === 'operation-set'" class="modal-grid">
      <label class="span-2">
        操作集名称
        <input v-model="dialogData.name" name="operation_set_name" maxlength="128" required autofocus autocomplete="off">
      </label>
      <label class="span-2">
        操作集说明
        <textarea v-model="dialogData.description" name="operation_set_description" maxlength="1000" autocomplete="off"></textarea>
      </label>
      <label class="span-2">
        执行器类型
        <select v-model="dialogData.executor_type" name="executor_type" required>
          <option value="HTTP">HTTP</option>
          <option value="POSTGRESQL">PostgreSQL</option>
          <option value="SIGN">Transit 签名</option>
        </select>
      </label>
    </div>

    <div v-else-if="dialogKind === 'operation'" class="modal-grid">
      <label v-if="!dialogContext?.operation">
        操作键
        <input v-model="dialogData.key" name="operation_key" pattern="[a-z0-9_]+" required autofocus autocomplete="off" spellcheck="false">
      </label>
      <label :class="{ 'span-2': dialogContext?.operation }">
        操作名称
        <input v-model="dialogData.name" name="operation_name" maxlength="128" required :autofocus="Boolean(dialogContext?.operation)" autocomplete="off">
      </label>
      <label class="span-2">
        操作说明
        <textarea v-model="dialogData.description" name="operation_description" maxlength="2000" autocomplete="off"></textarea>
      </label>
      <label class="span-2">
        风险等级
        <select v-model="dialogData.risk_level" name="risk_level" required>
          <option value="LOW">LOW</option>
          <option value="MEDIUM">MEDIUM</option>
          <option value="HIGH">HIGH</option>
        </select>
      </label>
      <label class="span-2">
        公开参数 Schema（JSON 对象）
        <textarea v-model="dialogData.arguments_schema" name="arguments_schema" class="code-input" required spellcheck="false"></textarea>
      </label>
      <label class="span-2">
        私有执行模板（JSON 对象）
        <textarea v-model="dialogData.execution_template" name="execution_template" class="code-input" required spellcheck="false"></textarea>
      </label>
    </div>

    <div v-else-if="dialogKind === 'binding'" class="modal-grid">
      <label class="span-2">
        目标
        <select v-model="dialogData.target_id" name="binding_target" :disabled="Boolean(dialogContext?.binding)" required :autofocus="!dialogContext?.binding">
          <option v-for="target in targets" :key="target.id" :value="target.id">{{ target.name }} · {{ target.id }}</option>
        </select>
      </label>
      <label class="span-2">
        精确版本
        <select v-model.number="dialogData.version" name="binding_version" required :autofocus="Boolean(dialogContext?.binding)">
          <option v-for="version in versionsFor(dialogContext?.operation?.id)" :key="version.version" :value="version.version">v{{ version.version }} · {{ version.name }}</option>
        </select>
      </label>
    </div>
  </ModalDialog>
</template>
