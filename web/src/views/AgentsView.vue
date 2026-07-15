<script setup>
import { nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatDate } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const token = ref('')
const tokenDialog = ref(null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    records.value = await props.api('/v1/web/agents')
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

async function registerAgent() {
  const name = window.prompt('Agent 名称')
  if (!name) return
  await mutate(async () => {
    const result = await props.api('/v1/web/agents', {
      method: 'POST',
      body: JSON.stringify({ name, token_lifetime: '24_HOURS' }),
    })
    await showToken(result.token)
  })
}

async function rotateAgent(agentID) {
  await mutate(async () => {
    const result = await props.api(`/v1/web/agents/${agentID}/rotate-token`, {
      method: 'POST',
      body: JSON.stringify({ token_lifetime: '24_HOURS' }),
    })
    await showToken(result.token)
  })
}

async function setActive(agentID, active) {
  await mutate(() => props.api(`/v1/web/agents/${agentID}`, {
    method: 'PATCH',
    body: JSON.stringify({ active }),
  }))
}

async function revokeToken(agentID) {
  await mutate(() => props.api(`/v1/web/agents/${agentID}/token`, {
    method: 'DELETE',
    body: '{}',
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

async function showToken(value) {
  token.value = value
  await nextTick()
  tokenDialog.value.showModal()
}

function clearToken() {
  token.value = ''
}

onMounted(load)
onBeforeUnmount(clearToken)
</script>

<template>
  <div class="toolbar">
    <p class="muted">Token 仅在注册或轮换时显示一次。</p>
    <button type="button" :disabled="busy" @click="registerAgent">注册 Agent</button>
  </div>
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <div v-else-if="records.length === 0" class="empty">暂无 Agent</div>
  <div v-else class="grid cards">
    <article v-for="record in records" :key="record.id" class="card">
      <span class="badge">{{ record.active ? '启用' : '停用' }}</span>
      <h2>{{ record.name }}</h2>
      <p class="muted">{{ record.token_expires_at ? `Token 到期：${formatDate(record.token_expires_at)}` : 'Token 已撤销或永久' }}</p>
      <div class="actions">
        <button type="button" class="secondary" :disabled="busy" @click="rotateAgent(record.id)">轮换 Token</button>
        <button type="button" class="secondary" :disabled="busy" @click="setActive(record.id, !record.active)">{{ record.active ? '停用' : '启用' }}</button>
        <button type="button" class="danger" :disabled="busy" @click="revokeToken(record.id)">撤销 Token</button>
      </div>
    </article>
  </div>
  <dialog ref="tokenDialog" @close="clearToken">
    <form method="dialog">
      <p class="eyebrow">仅显示一次</p>
      <h2>交付 Agent Token</h2>
      <p>本地 MVP 请保存到根目录 .agent-token，并设置为 0600；该文件已被 Git 忽略。不要写入 Prompt、其他文件或日志；关闭后无法再次查看。</p>
      <pre>{{ token }}</pre>
      <button type="submit">我已安全交付</button>
    </form>
  </dialog>
</template>
