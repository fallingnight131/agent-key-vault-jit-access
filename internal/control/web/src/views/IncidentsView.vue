<script setup>
import { onMounted, ref } from 'vue'
import { formatDate } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    records.value = await props.api('/v1/web/incidents')
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

async function resolveIncident(incidentID) {
  busy.value = true
  error.value = ''
  try {
    await props.api(`/v1/web/incidents/${incidentID}/resolve`, { method: 'POST', body: '{}' })
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
  <p v-if="loading" class="muted">正在加载…</p>
  <p v-else-if="error" class="error" role="alert">{{ error }}</p>
  <div v-else-if="records.length === 0" class="empty">暂无安全告警</div>
  <div v-else class="grid cards">
    <article v-for="item in records" :key="item.id" class="card">
      <span class="badge">{{ item.status }}</span>
      <h2>{{ item.error_code }}</h2>
      <p class="muted">{{ formatDate(item.created_at) }}</p>
      <button v-if="item.status === 'OPEN'" type="button" class="danger" :disabled="busy" @click="resolveIncident(item.id)">确认已人工处置</button>
    </article>
  </div>
</template>
