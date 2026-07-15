<script setup>
import { ref } from 'vue'

const props = defineProps({
  login: { type: Function, required: true },
  error: { type: String, default: '' },
})

const username = ref('')
const password = ref('')
const submitting = ref(false)

async function submit() {
  submitting.value = true
  try {
    await props.login({ username: username.value, password: password.value })
    password.value = ''
  } catch {
    // App owns the public error message; keep the password for a quick correction.
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-shell">
    <section class="login-card" aria-labelledby="login-title">
      <div class="brand-mark">AKV</div>
      <p class="eyebrow">Agent Key Vault</p>
      <h1 id="login-title">人工授权，秘密不离开边界</h1>
      <p class="muted">登录以审批一次性操作、管理 Agent 并查看完整审计链。</p>
      <form @submit.prevent="submit">
        <label>
          用户名
          <input v-model="username" name="username" autocomplete="username" required>
        </label>
        <label>
          密码
          <input v-model="password" name="password" type="password" autocomplete="current-password" required>
        </label>
        <button type="submit" :disabled="submitting">
          {{ submitting ? '正在登录…' : '进入控制台' }}
        </button>
        <p class="error" role="alert">{{ error }}</p>
      </form>
    </section>
  </main>
</template>
