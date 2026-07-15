<script setup>
import { onMounted, ref } from 'vue'
import { createAPI } from './api.js'
import LoginView from './components/LoginView.vue'
import WorkspaceShell from './components/WorkspaceShell.vue'

const currentUser = ref(null)
const booting = ref(true)
const loginError = ref('')

const api = createAPI({
  onUnauthorized: () => {
    currentUser.value = null
  },
})

async function loadSession() {
  try {
    currentUser.value = await api('/v1/web/me')
  } catch {
    currentUser.value = null
  } finally {
    booting.value = false
  }
}

async function login(credentials) {
  loginError.value = ''
  try {
    await api('/v1/web/login', {
      method: 'POST',
      body: JSON.stringify(credentials),
    })
    currentUser.value = await api('/v1/web/me')
  } catch (error) {
    loginError.value = error.message
    throw error
  }
}

async function logout() {
  try {
    await api('/v1/web/logout', { method: 'POST', body: '{}' })
  } finally {
    currentUser.value = null
  }
}

onMounted(loadSession)
</script>

<template>
  <main v-if="booting" class="boot-shell" aria-live="polite">
    <div class="brand-mark">AKV</div>
    <p class="muted">正在连接控制面…</p>
  </main>
  <LoginView v-else-if="!currentUser" :login="login" :error="loginError" />
  <WorkspaceShell v-else :user="currentUser" :api="api" :logout="logout" />
</template>
