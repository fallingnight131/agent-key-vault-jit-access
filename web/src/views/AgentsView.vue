<script setup>
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { formatDate } from '../helpers.js'
import ModalDialog from '../components/ModalDialog.vue'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busy = ref(false)
const dialogKind = ref('')
const dialogAgent = ref(null)
const dialogError = ref('')
const agentName = ref('')
const token = ref('')

const dialogConfig = computed(() => {
  if (dialogKind.value === 'register') {
    return {
      title: '注册 Agent',
      description: '创建一个新的 Agent 身份。Token 有效期为 24 小时，只会显示一次。',
      submitLabel: '注册并生成 Token',
    }
  }
  if (dialogKind.value === 'rotate') {
    return {
      title: '轮换 Agent Token',
      description: `将为 ${dialogAgent.value?.name || '这个 Agent'} 生成新 Token，旧 Token 会立即失效。`,
      submitLabel: '确认轮换',
      danger: true,
      closeOnBackdrop: true,
    }
  }
  if (dialogKind.value === 'revoke') {
    return {
      title: '撤销 Agent Token',
      description: `撤销后，${dialogAgent.value?.name || '这个 Agent'} 将无法继续调用 AKV，直到重新轮换 Token。`,
      submitLabel: '确认撤销',
      danger: true,
      closeOnBackdrop: true,
    }
  }
  return {
    title: '保存 Agent Token',
    description: '这是完整 Token 唯一一次展示。保存后关闭弹窗，控制台不会再次提供查看。',
    submitLabel: '我已安全保存',
    showCancel: false,
    closeOnBackdrop: false,
    dismissible: false,
  }
})

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

function openRegister() {
  clearDialogState()
  dialogKind.value = 'register'
}

function openRotate(record) {
  clearDialogState()
  dialogAgent.value = record
  dialogKind.value = 'rotate'
}

function openRevoke(record) {
  if (!record.has_active_token) return
  clearDialogState()
  dialogAgent.value = record
  dialogKind.value = 'revoke'
}

async function submitDialog() {
  if (dialogKind.value === 'token') {
    closeDialog()
    return
  }
  if (dialogKind.value === 'register' && !agentName.value.trim()) {
    dialogError.value = '请输入 Agent 名称'
    return
  }

  busy.value = true
  dialogError.value = ''
  notice.value = ''
  try {
    if (dialogKind.value === 'register') {
      const result = await props.api('/v1/web/agents', {
        method: 'POST',
        body: JSON.stringify({ name: agentName.value.trim(), token_lifetime: '24_HOURS' }),
      })
      busy.value = false
      showToken(result.token)
    } else if (dialogKind.value === 'rotate') {
      const result = await props.api(`/v1/web/agents/${dialogAgent.value.id}/rotate-token`, {
        method: 'POST',
        body: JSON.stringify({ token_lifetime: '24_HOURS' }),
      })
      busy.value = false
      showToken(result.token)
    } else if (dialogKind.value === 'revoke') {
      await props.api(`/v1/web/agents/${dialogAgent.value.id}/token`, { method: 'DELETE' })
      notice.value = `${dialogAgent.value.name} 的 Token 已撤销`
      closeDialog(true)
    }
    await load()
  } catch (failure) {
    dialogError.value = failure.message
  } finally {
    busy.value = false
  }
}

async function setActive(agentID, active) {
  await mutate(() => props.api(`/v1/web/agents/${agentID}`, {
    method: 'PATCH',
    body: JSON.stringify({ active }),
  }))
}

async function mutate(operation) {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await operation()
    await load()
  } catch (failure) {
    error.value = failure.message
  } finally {
    busy.value = false
  }
}

function showToken(value) {
  agentName.value = ''
  dialogAgent.value = null
  dialogError.value = ''
  token.value = value
  dialogKind.value = 'token'
}

function closeDialog(force = false) {
  if (busy.value && !force) return
  dialogKind.value = ''
  clearDialogState()
}

function clearDialogState() {
  agentName.value = ''
  token.value = ''
  dialogAgent.value = null
  dialogError.value = ''
}

function tokenStatus(record) {
  if (!record.has_active_token) return 'Token 已撤销'
  if (!record.token_expires_at) return 'Token 永久有效'
  if (new Date(record.token_expires_at).getTime() <= Date.now()) {
    return `Token 已过期：${formatDate(record.token_expires_at)}`
  }
  return `Token 到期：${formatDate(record.token_expires_at)}`
}

onMounted(load)
onBeforeUnmount(clearDialogState)
</script>

<template>
  <div class="toolbar">
    <p class="muted">Token 仅在注册或轮换时显示一次。</p>
    <button type="button" :disabled="busy" @click="openRegister">注册 Agent</button>
  </div>
  <p v-if="notice" class="success" role="status">{{ notice }}</p>
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <div v-else-if="records.length === 0" class="empty">暂无 Agent</div>
  <div v-else class="grid cards">
    <article v-for="record in records" :key="record.id" class="card">
      <span class="badge">{{ record.active ? '启用' : '停用' }}</span>
      <h2>{{ record.name }}</h2>
      <p class="muted">{{ tokenStatus(record) }}</p>
      <div class="actions">
        <button type="button" class="secondary" :disabled="busy" @click="openRotate(record)">轮换 Token</button>
        <button type="button" class="secondary" :disabled="busy" @click="setActive(record.id, !record.active)">{{ record.active ? '停用' : '启用' }}</button>
        <button v-if="record.has_active_token" type="button" class="danger" :disabled="busy" @click="openRevoke(record)">撤销 Token</button>
      </div>
    </article>
  </div>

  <ModalDialog
    :open="Boolean(dialogKind)"
    :title="dialogConfig.title"
    :description="dialogConfig.description"
    :content-key="dialogKind"
    :submit-label="dialogConfig.submitLabel"
    :show-cancel="dialogConfig.showCancel !== false"
    :close-on-backdrop="Boolean(dialogConfig.closeOnBackdrop)"
    :dismissible="dialogConfig.dismissible !== false"
    :danger="dialogConfig.danger"
    :busy="busy"
    :error="dialogError"
    @close="closeDialog"
    @submit="submitDialog"
  >
    <div v-if="dialogKind === 'register'" class="modal-grid">
      <label class="span-2">
        Agent 名称
        <input v-model="agentName" name="agent_name" maxlength="128" required autofocus autocomplete="off">
      </label>
    </div>
    <p v-else-if="dialogKind === 'rotate'" class="modal-warning">
      旧 Token 会立即失效。生成新 Token 后，请同步覆盖项目根目录的 .agent-token。
    </p>
    <p v-else-if="dialogKind === 'revoke'" class="modal-warning">
      已经建立的任务会按现有生命周期处理，但这个 Agent 后续的认证请求会被拒绝。
    </p>
    <template v-else-if="dialogKind === 'token'">
      <p>本地 MVP 请保存到根目录 <code>.agent-token</code>，并设置为 <code>0600</code>。该文件已被 Git 忽略。</p>
      <pre class="modal-token">{{ token }}</pre>
      <p class="muted">不要写入 Prompt、其他文件或日志。</p>
    </template>
  </ModalDialog>
</template>
