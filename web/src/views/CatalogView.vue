<script setup>
import { onMounted, ref } from 'vue'
import { encodeBase64 } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const targets = ref([])
const credentials = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await props.api('/v1/web/catalog')
    targets.value = data.targets
    credentials.value = data.credentials
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

async function mutate(operation) {
  busy.value = true
  error.value = ''
  try {
    await operation()
    await load()
  } catch (failure) {
    error.value = failure.message
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
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <template v-else>
    <div v-if="targets.length" class="grid cards">
      <article v-for="target in targets" :key="target.id" class="card">
        <span class="badge">{{ target.active ? '启用' : '停用' }}</span>
        <h2>{{ target.name }}</h2>
        <p class="muted">{{ target.connector_type }} · {{ target.description || '无描述' }}</p>
        <button type="button" class="secondary" :disabled="busy" @click="toggleTarget(target)">{{ target.active ? '停用' : '启用' }}</button>
      </article>
    </div>
    <div v-else class="empty">暂无目标</div>
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
