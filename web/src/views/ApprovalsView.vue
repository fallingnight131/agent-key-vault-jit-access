<script setup>
import { computed, onMounted, ref } from 'vue'
import { formatDate, prettyJSON } from '../helpers.js'
import ModalDialog from '../components/ModalDialog.vue'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const auditEvents = ref(null)
const loading = ref(true)
const error = ref('')
const busy = ref(false)
const dialogKind = ref('')
const dialogRecord = ref(null)
const dialogError = ref('')
const grantMinutes = ref(10)

const dialogConfig = computed(() => {
  if (dialogKind.value === 'approve') {
    return {
      title: '批准一次性授权',
      description: '设置 Grant 必须开始执行的时限。到期后即使 Agent 尚未执行，也必须重新申请。',
      submitLabel: '确认批准',
    }
  }
  if (dialogKind.value === 'reject') {
    return {
      title: '拒绝授权申请',
      description: '拒绝后不会创建 Grant，Agent 必须重新提交新的申请。',
      submitLabel: '确认拒绝',
      danger: true,
    }
  }
  return {
    title: '撤销一次性授权',
    description: '尚未执行的 Grant 会被阻止；执行中的操作只会进行尽力取消。',
    submitLabel: '确认撤销',
    danger: true,
  }
})

async function load() {
  loading.value = true
  error.value = ''
  auditEvents.value = null
  try {
    records.value = await props.api('/v1/web/authorizations')
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

function openDecision(record, decision) {
  dialogRecord.value = record
  dialogError.value = ''
  grantMinutes.value = 10
  dialogKind.value = decision === 'APPROVED' ? 'approve' : 'reject'
}

function openRevoke(record) {
  dialogRecord.value = record
  dialogError.value = ''
  dialogKind.value = 'revoke'
}

async function submitDialog() {
  const record = dialogRecord.value
  if (!record) return

  let path
  let body
  if (dialogKind.value === 'approve') {
    const minutes = Number(grantMinutes.value)
    if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10) {
      dialogError.value = '请输入 1 到 10 的整数分钟'
      return
    }
    path = `/v1/web/authorizations/${record.request_id}/decision`
    body = { decision: 'APPROVED', grant_ttl_seconds: minutes * 60 }
  } else if (dialogKind.value === 'reject') {
    path = `/v1/web/authorizations/${record.request_id}/decision`
    body = { decision: 'REJECTED' }
  } else {
    path = `/v1/web/authorizations/${record.request_id}/revoke`
    body = {}
  }

  busy.value = true
  dialogError.value = ''
  try {
    await props.api(path, { method: 'POST', body: JSON.stringify(body) })
    closeDialog(true)
    await load()
  } catch (failure) {
    dialogError.value = failure.message
  } finally {
    busy.value = false
  }
}

function closeDialog(force = false) {
  if (busy.value && !force) return
  dialogKind.value = ''
  dialogRecord.value = null
  dialogError.value = ''
  grantMinutes.value = 10
}

async function showAudit(requestID) {
  loading.value = true
  error.value = ''
  try {
    auditEvents.value = await props.api(`/v1/web/authorizations/${requestID}/audit`)
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <template v-else-if="auditEvents">
    <button type="button" class="secondary" @click="load">返回申请</button>
    <div v-if="auditEvents.length" class="grid audit-grid">
      <article v-for="event in auditEvents" :key="event.id" class="card">
        <h2>{{ event.event_type }}</h2>
        <p class="muted">{{ formatDate(event.created_at) }}</p>
        <pre class="operation">{{ prettyJSON(event.metadata) }}</pre>
      </article>
    </div>
    <div v-else class="empty">暂无关联审计事件</div>
  </template>
  <div v-else-if="records.length === 0" class="empty">暂无授权申请</div>
  <div v-else class="grid cards">
    <article v-for="record in records" :key="record.request_id" class="card">
      <span class="badge">{{ record.status }}</span>
      <h2>{{ record.agent_name }} → {{ record.target_name }}</h2>
      <dl class="meta">
        <dt>所属用户</dt><dd>{{ record.owner_username }}</dd>
        <dt>Agent ID</dt><dd>{{ record.agent_id }}</dd>
        <dt>任务</dt><dd>{{ record.task_id }}</dd>
        <dt>凭证</dt><dd>{{ record.credential_alias }} · {{ record.credential_type }}</dd>
        <template v-if="record.operation_id">
          <dt>安全操作</dt><dd>{{ record.operation_name }}（{{ record.operation_key }}）</dd>
          <dt>操作版本</dt><dd>{{ record.operation_id }} · v{{ record.version }}</dd>
          <dt>业务参数</dt><dd><pre class="operation">{{ prettyJSON(record.arguments) }}</pre></dd>
        </template>
        <dt>原因</dt><dd>{{ record.reason }}</dd>
        <dt>申请截止</dt><dd>{{ formatDate(record.approval_deadline) }}</dd>
        <dt>授权有效期</dt>
        <dd>{{ record.grant_expires_at ? formatDate(record.grant_expires_at) : '批准后最长 10 分钟内必须开始' }}</dd>
      </dl>
      <p class="risk">{{ record.risk_hint }}</p>
      <p class="muted">AKV 根据已发布模板编译并冻结的实际执行效果：</p>
      <pre class="operation">{{ prettyJSON(record.operation) }}</pre>
      <div class="actions">
        <template v-if="record.status === 'PENDING_APPROVAL'">
          <button type="button" :disabled="busy" @click="openDecision(record, 'APPROVED')">批准</button>
          <button type="button" class="danger" :disabled="busy" @click="openDecision(record, 'REJECTED')">拒绝</button>
        </template>
        <button type="button" class="secondary" :disabled="busy" @click="showAudit(record.request_id)">审计</button>
        <button v-if="record.status === 'APPROVED'" type="button" class="danger" :disabled="busy" @click="openRevoke(record)">撤销</button>
      </div>
    </article>
  </div>

  <ModalDialog
    :open="Boolean(dialogKind)"
    :title="dialogConfig.title"
    :description="dialogConfig.description"
    :submit-label="dialogConfig.submitLabel"
    :danger="dialogConfig.danger"
    :close-on-backdrop="dialogKind !== 'approve'"
    :busy="busy"
    :error="dialogError"
    @close="closeDialog"
    @submit="submitDialog"
  >
    <div v-if="dialogKind === 'approve'" class="modal-grid">
      <label class="span-2">
        必须开始执行的时限（分钟）
        <input v-model.number="grantMinutes" name="grant_minutes" type="number" min="1" max="10" step="1" required autofocus>
      </label>
      <p class="field-help span-2">允许 1–10 分钟。Grant 一旦执行或过期都不能重复使用。</p>
    </div>
    <p v-else class="modal-warning">
      {{ dialogRecord?.agent_name }} → {{ dialogRecord?.target_name }}，操作完成后无法通过这个弹窗恢复。
    </p>
  </ModalDialog>
</template>
