<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  login: { type: Function, required: true },
  register: { type: Function, required: true },
  error: { type: String, default: '' },
})
const emit = defineEmits(['clear-error'])

const mode = ref('login')
const username = ref('')
const password = ref('')
const passwordConfirmation = ref('')
const submitting = ref(false)
const localError = ref('')

const isRegistering = computed(() => mode.value === 'register')
const displayedError = computed(() => localError.value || props.error)

function switchMode() {
  if (submitting.value) return
  mode.value = isRegistering.value ? 'login' : 'register'
  password.value = ''
  passwordConfirmation.value = ''
  localError.value = ''
  emit('clear-error')
}

async function submit() {
  if (submitting.value) return
  localError.value = ''
  emit('clear-error')
  if (isRegistering.value && password.value !== passwordConfirmation.value) {
    localError.value = '两次输入的密码不一致'
    return
  }
  submitting.value = true
  try {
    const action = isRegistering.value ? props.register : props.login
    await action({ username: username.value, password: password.value })
    password.value = ''
    passwordConfirmation.value = ''
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
      <p class="muted">
        {{ isRegistering
          ? '注册普通用户账号，开始管理你的 Agent 和授权申请。'
          : '登录以审批一次性操作、管理 Agent 并查看完整审计链。' }}
      </p>
      <form @submit.prevent="submit">
        <label>
          用户名
          <input
            v-model="username"
            name="username"
            autocomplete="username"
            :maxlength="isRegistering ? 64 : undefined"
            required
          >
        </label>
        <label>
          密码
          <input
            v-model="password"
            name="password"
            type="password"
            :autocomplete="isRegistering ? 'new-password' : 'current-password'"
            :minlength="isRegistering ? 8 : undefined"
            :aria-describedby="isRegistering ? 'registration-password-help' : undefined"
            maxlength="72"
            required
          >
        </label>
        <label v-if="isRegistering">
          再次输入密码
          <input
            v-model="passwordConfirmation"
            name="password_confirmation"
            type="password"
            autocomplete="new-password"
            minlength="8"
            maxlength="72"
            aria-describedby="registration-password-help"
            required
          >
        </label>
        <p v-if="isRegistering" id="registration-password-help" class="muted auth-help">密码至少需要 8 个字符。</p>
        <button type="submit" :disabled="submitting">
          {{ submitting
            ? (isRegistering ? '正在注册…' : '正在登录…')
            : (isRegistering ? '创建账号' : '进入控制台') }}
        </button>
        <button type="button" class="secondary auth-switch" :disabled="submitting" @click="switchMode">
          {{ isRegistering ? '返回登录' : '注册账号' }}
        </button>
        <p class="error" role="alert">{{ displayedError }}</p>
      </form>
    </section>
  </main>
</template>
