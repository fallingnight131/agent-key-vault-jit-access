<script setup>
import { computed, ref } from 'vue'
import { roleLabel } from '../helpers.js'
import ApprovalsView from '../views/ApprovalsView.vue'
import AgentsView from '../views/AgentsView.vue'
import CatalogView from '../views/CatalogView.vue'
import UsersView from '../views/UsersView.vue'
import AuditView from '../views/AuditView.vue'
import IncidentsView from '../views/IncidentsView.vue'

const props = defineProps({
  user: { type: Object, required: true },
  api: { type: Function, required: true },
  logout: { type: Function, required: true },
})

const activeView = ref('approvals')
const loggingOut = ref(false)
const views = {
  approvals: { title: '授权申请', label: '待审批', component: ApprovalsView },
  agents: { title: 'Agent 身份', label: 'Agent', component: AgentsView },
  catalog: { title: '目标与凭证', label: '目标与凭证', component: CatalogView, admin: true },
  users: { title: '用户权限', label: '用户', component: UsersView, admin: true },
  audit: { title: '全局审计', label: '全局审计', component: AuditView, admin: true },
  incidents: { title: '安全告警', label: '安全告警', component: IncidentsView, admin: true },
}
const navigation = computed(() => Object.entries(views).filter(([, view]) => !view.admin || props.user.is_admin))
const current = computed(() => views[activeView.value])

async function signOut() {
  loggingOut.value = true
  try {
    await props.logout()
  } finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <div class="app-shell">
    <aside>
      <div class="wordmark"><span>AKV</span><small>CONTROL PLANE</small></div>
      <nav aria-label="主导航">
        <button
          v-for="([name, view]) in navigation"
          :key="name"
          type="button"
          :class="{ active: activeView === name }"
          @click="activeView = name"
        >
          {{ view.label }}
        </button>
      </nav>
      <div class="identity">
        <strong>{{ user.username }}</strong>
        <span>{{ roleLabel(user) }}</span>
        <button type="button" class="quiet" :disabled="loggingOut" @click="signOut">退出</button>
      </div>
    </aside>
    <main class="workspace">
      <header>
        <div>
          <p class="eyebrow">HUMAN CONTROL PLANE</p>
          <h1>{{ current.title }}</h1>
        </div>
        <div class="status-pill">已连接</div>
      </header>
      <section aria-live="polite">
        <component :is="current.component" :api="api" />
      </section>
    </main>
  </div>
</template>
