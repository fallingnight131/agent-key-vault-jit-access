<script setup>
import { onMounted, ref } from 'vue'
import { encodeBase64 } from '../helpers.js'

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
const definitionDrafts = new Map()

const emptyArgumentsSchema = {
  type: 'object',
  properties: {},
  required: [],
  additionalProperties: false,
}

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

function secretPayload(type) {
  if (type === 'API_KEY') return singleSecret('API Key（只写入一次）', 'api_key')
  if (type === 'ACCESS_TOKEN') return singleSecret('Access Token（只写入一次）', 'access_token')
  if (type === 'USERNAME_PASSWORD') {
    let username = window.prompt('目标用户名')
    let password = window.prompt('目标密码（只写入一次）')
    if (!username || !password) return null
    const payload = { secret_values: { username: encodeBase64(username), password: encodeBase64(password) } }
    username = ''
    password = ''
    return payload
  }
  if (type === 'CERTIFICATE') {
    let certificate = window.prompt('PEM 证书（仅存储）')
    let privateKey = window.prompt('PEM 私钥（仅存储）')
    if (!certificate || !privateKey) return null
    const payload = { secret_values: { certificate: encodeBase64(certificate), private_key: encodeBase64(privateKey) } }
    certificate = ''
    privateKey = ''
    return payload
  }
  if (type === 'TRANSIT_KEY') {
    const keyType = window.prompt('Transit Key 类型', 'ecdsa-p256')
    return keyType ? { transit_key_type: keyType } : null
  }
  if (type === 'POSTGRESQL_DYNAMIC') {
    const connectionName = window.prompt('OpenBao database connection name')
    const creation = window.prompt('创建临时用户的 SQL 语句')
    return connectionName && creation ? {
      database_role: {
        connection_name: connectionName,
        creation_statements: [creation],
        default_ttl: 60000000000,
        max_ttl: 300000000000,
      },
    } : null
  }
  return null
}

function singleSecret(message, key) {
  let value = window.prompt(message)
  if (!value) return null
  const payload = { secret_values: { [key]: encodeBase64(value) } }
  value = ''
  return payload
}

async function createHTTPTarget() {
  const name = window.prompt('目标名称')
  const baseURL = window.prompt('HTTP(S) 基础 URL')
  const credentialType = window.prompt('凭证类型：API_KEY / ACCESS_TOKEN / USERNAME_PASSWORD / CERTIFICATE / TRANSIT_KEY', 'API_KEY')?.toUpperCase()
  if (!name || !baseURL || !credentialType) return
  const material = secretPayload(credentialType)
  if (!material) return
  await mutate(() => props.api('/v1/web/targets', {
    method: 'POST',
    body: JSON.stringify({
      name,
      description: '',
      connector_type: 'HTTP',
      connection_config: { base_url: baseURL, allowed_http_methods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] },
      credential_alias: 'default',
      credential_type: credentialType,
      ...material,
    }),
  }))
}

async function createPostgreSQLTarget() {
  const name = window.prompt('目标名称')
  const host = window.prompt('数据库主机名或 IP')
  const port = Number(window.prompt('端口', '5432'))
  const database = window.prompt('数据库名')
  const dynamic = window.confirm('使用 OpenBao 动态 PostgreSQL 凭证？')
  if (!name || !host || !port || !database) return
  const credentialType = dynamic ? 'POSTGRESQL_DYNAMIC' : 'USERNAME_PASSWORD'
  const material = secretPayload(credentialType)
  if (!material) return
  await mutate(() => props.api('/v1/web/targets', {
    method: 'POST',
    body: JSON.stringify({
      name,
      description: '',
      connector_type: 'POSTGRESQL',
      connection_config: { host, port, database, tls_mode: 'require', require_dynamic: dynamic },
      credential_alias: 'default',
      credential_type: credentialType,
      ...material,
    }),
  }))
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

async function rotateCredential(credential) {
  const material = secretPayload(credential.credential_type)
  if (!material) return
  await mutate(() => props.api(`/v1/web/credentials/${credential.id}`, {
    method: 'PATCH',
    body: JSON.stringify(material),
  }))
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
    error.value = `${label}必须是有效的 JSON 对象`
    return null
  }
}

function readOperationDefinition(set, operation = null) {
  error.value = ''
  const draftKey = operation ? `version:${operation.id}` : `operation:${set.id}`
  const current = operation ? currentVersionFor(operation) : null
  const saved = definitionDrafts.get(draftKey)
  const draft = saved || {
    key: '',
    name: current?.name || '',
    description: current?.description || '',
    riskLevel: current?.risk_level || 'LOW',
    argumentsSchema: prettyJSON(current?.arguments_schema || defaultSchema(set.executor_type)),
    executionTemplate: prettyJSON(current?.execution_template || defaultTemplate(set.executor_type)),
  }

  if (!operation) {
    const key = window.prompt('操作键：小写字母、数字或下划线', draft.key)
    if (key === null) return null
    draft.key = key.trim()
  }
  const name = window.prompt('操作名称', draft.name)
  if (name === null) return null
  draft.name = name.trim()
  const description = window.prompt('操作说明', draft.description)
  if (description === null) return null
  draft.description = description.trim()
  const riskLevel = window.prompt('风险等级：LOW / MEDIUM / HIGH', draft.riskLevel)
  if (riskLevel === null) return null
  draft.riskLevel = riskLevel.trim().toUpperCase()
  const argumentsSchema = window.prompt('公开参数 Schema（JSON 对象）', draft.argumentsSchema)
  if (argumentsSchema === null) return null
  draft.argumentsSchema = argumentsSchema
  const executionTemplate = window.prompt('私有执行模板（JSON 对象）', draft.executionTemplate)
  if (executionTemplate === null) return null
  draft.executionTemplate = executionTemplate
  definitionDrafts.set(draftKey, draft)

  if ((!operation && !draft.key) || !draft.name || !['LOW', 'MEDIUM', 'HIGH'].includes(draft.riskLevel)) {
    error.value = '请填写操作键、操作名称和正确的风险等级'
    return null
  }
  const parsedSchema = parseJSONObject('公开参数 Schema', draft.argumentsSchema)
  if (!parsedSchema) return null
  const parsedTemplate = parseJSONObject('私有执行模板', draft.executionTemplate)
  if (!parsedTemplate) return null
  return {
    draftKey,
    payload: {
      ...(!operation ? { key: draft.key } : {}),
      name: draft.name,
      description: draft.description,
      risk_level: draft.riskLevel,
      arguments_schema: parsedSchema,
      execution_template: parsedTemplate,
    },
  }
}

async function createOperationSet() {
  error.value = ''
  const name = window.prompt('操作集名称')
  if (name === null) return
  const description = window.prompt('操作集说明', '')
  if (description === null) return
  const executorType = window.prompt('执行器类型：HTTP / POSTGRESQL / SIGN', 'HTTP')?.trim().toUpperCase()
  if (!name.trim() || !executorType || !['HTTP', 'POSTGRESQL', 'SIGN'].includes(executorType)) {
    error.value = '请填写操作集名称和正确的执行器类型'
    return
  }
  await mutate(() => props.api('/v1/web/operation-sets', {
    method: 'POST',
    body: JSON.stringify({ name: name.trim(), description: description.trim(), executor_type: executorType }),
  }))
}

async function createOperation(set) {
  const definition = readOperationDefinition(set)
  if (!definition) return
  const succeeded = await mutate(() => props.api(`/v1/web/operation-sets/${set.id}/operations`, {
    method: 'POST',
    body: JSON.stringify(definition.payload),
  }))
  if (succeeded) definitionDrafts.delete(definition.draftKey)
}

async function publishOperationVersion(set, operation) {
  const definition = readOperationDefinition(set, operation)
  if (!definition) return
  const succeeded = await mutate(() => props.api(`/v1/web/operations/${operation.id}/versions`, {
    method: 'POST',
    body: JSON.stringify(definition.payload),
  }))
  if (succeeded) definitionDrafts.delete(definition.draftKey)
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

function readVersion(operation, initialVersion) {
  const available = versionsFor(operation.id).map((version) => version.version)
  const raw = window.prompt(`精确版本号（可用：${available.map((version) => `v${version}`).join('、')}）`, String(initialVersion))
  if (raw === null) return null
  const version = Number(raw)
  if (!Number.isInteger(version) || !available.includes(version)) {
    error.value = '请输入已发布的精确版本号'
    return null
  }
  return version
}

async function bindOperation(operation) {
  error.value = ''
  if (!targets.value.length) {
    error.value = '请先创建可绑定的目标'
    return
  }
  const choices = targets.value.map((target) => `${target.name}: ${target.id}`).join('\n')
  const targetID = window.prompt(`输入目标 ID：\n${choices}`, targets.value[0].id)
  if (targetID === null) return
  if (!targets.value.some((target) => target.id === targetID.trim())) {
    error.value = '请输入列表中的目标 ID'
    return
  }
  const version = readVersion(operation, operation.current_version)
  if (version === null) return
  await saveBinding(targetID.trim(), operation.id, version, true)
}

async function upgradeBinding(binding) {
  error.value = ''
  const operation = operationFor(binding.operation_id)
  if (!operation) {
    error.value = '找不到这条绑定对应的操作'
    return
  }
  const version = readVersion(operation, binding.version)
  if (version === null) return
  await saveBinding(binding.target_id, binding.operation_id, version, binding.active)
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
</template>
