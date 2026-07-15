<script setup>
import { onMounted, ref } from 'vue'
import { formatDate } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    records.value = await props.api('/v1/web/audit')
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <div v-else-if="records.length === 0" class="empty">暂无审计事件</div>
  <div v-else class="table-wrap">
    <table>
      <thead><tr><th>时间</th><th>事件</th><th>Actor 类型</th><th>Actor ID</th><th>Request ID</th><th>状态</th></tr></thead>
      <tbody>
        <tr v-for="event in records" :key="event.id">
          <td>{{ formatDate(event.created_at) }}</td>
          <td>{{ event.event_type }}</td>
          <td>{{ event.actor_type }}</td>
          <td>{{ event.actor_id || '—' }}</td>
          <td>{{ event.request_id || '—' }}</td>
          <td>{{ JSON.stringify(event.metadata) }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
