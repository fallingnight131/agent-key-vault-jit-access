<script setup>
import { onMounted, ref } from 'vue'
import { roleLabel } from '../helpers.js'

const props = defineProps({ api: { type: Function, required: true } })
const records = ref([])
const loading = ref(true)
const error = ref('')
const busy = ref(false)

async function load() {
  loading.value = true
  error.value = ''
  try {
    records.value = await props.api('/v1/web/users')
  } catch (failure) {
    error.value = failure.message
  } finally {
    loading.value = false
  }
}

async function updateUser(user, active, approveAll) {
  busy.value = true
  error.value = ''
  try {
    await props.api(`/v1/web/users/${user.id}`, {
      method: 'PATCH',
      body: JSON.stringify({ active, approve_all: approveAll }),
    })
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
  <div v-else-if="records.length === 0" class="empty">暂无用户</div>
  <div v-else class="grid cards">
    <article v-for="user in records" :key="user.id" class="card">
      <span class="badge">{{ user.owner_active ? '启用' : '停用' }}</span>
      <h2>{{ user.username }}</h2>
      <p class="muted">{{ roleLabel(user) }}</p>
      <div v-if="!user.is_admin" class="actions">
        <button type="button" class="secondary" :disabled="busy" @click="updateUser(user, user.owner_active, !user.approve_all)">{{ user.approve_all ? '撤销全局审批' : '授予全局审批' }}</button>
        <button type="button" class="danger" :disabled="busy" @click="updateUser(user, !user.owner_active, user.approve_all)">{{ user.owner_active ? '停用' : '启用' }}</button>
      </div>
    </article>
  </div>
</template>
