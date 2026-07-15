<script setup>
import { onMounted, ref } from 'vue'
import { formatDate, prettyJSON } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const auditEvents = ref(null)
const loading = ref(true)
const error = ref('')
const busy = ref(false)

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

async function decide(requestID, decision) {
  const body = { decision }
  if (decision === 'APPROVED') {
    const minutes = window.prompt('授权必须开始的时限（分钟，1-10）', '10')
    if (minutes === null) return
    const parsed = Number(minutes)
    if (!Number.isInteger(parsed) || parsed < 1 || parsed > 10) {
      window.alert('请输入 1 到 10 的整数分钟。')
      return
    }
    body.grant_ttl_seconds = parsed * 60
  }
  await mutate(() => props.api(`/v1/web/authorizations/${requestID}/decision`, {
    method: 'POST',
    body: JSON.stringify(body),
  }))
}

async function revoke(requestID) {
  await mutate(() => props.api(`/v1/web/authorizations/${requestID}/revoke`, {
    method: 'POST',
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
          <button type="button" :disabled="busy" @click="decide(record.request_id, 'APPROVED')">批准</button>
          <button type="button" class="danger" :disabled="busy" @click="decide(record.request_id, 'REJECTED')">拒绝</button>
        </template>
        <button type="button" class="secondary" :disabled="busy" @click="showAudit(record.request_id)">审计</button>
        <button v-if="record.status === 'APPROVED'" type="button" class="danger" :disabled="busy" @click="revoke(record.request_id)">撤销</button>
      </div>
    </article>
  </div>
</template>
