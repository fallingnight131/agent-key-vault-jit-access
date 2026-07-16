import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ApprovalsView from '../src/views/ApprovalsView.vue'

function approvalRecord() {
  return {
    request_id: 'request-1',
    status: 'PENDING_APPROVAL',
    agent_name: '排障 Agent',
    target_name: '工单 API',
    owner_username: 'owner',
    agent_id: 'agent-1',
    task_id: 'task-1',
    credential_alias: 'default',
    credential_type: 'API_KEY',
    reason: '排查工单问题',
    approval_deadline: '2026-07-16T12:00:00Z',
    grant_expires_at: null,
    risk_hint: '只读操作',
    operation: { kind: 'HTTP', method: 'GET', path: '/tickets/1' },
  }
}

function metricSummary(overrides = {}) {
  return {
    capture_status: 'ACTIVE',
    request_to_result_duration_ms: 180000,
    manual_handoff_count: 2,
    approval_followup_count: 1,
    operation_review_duration_ms: 90000,
    review_status: 'COMPLETED',
    review_session_id: 'review-1',
    improvement_target_percent: null,
    can_record_manual_handoff: true,
    can_record_approval_followup: true,
    can_start_review: true,
    can_complete_review: true,
    ...overrides,
  }
}

function findButton(wrapper, label) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)
  expect(button, `missing button: ${label}`).toBeTruthy()
  return button
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('授权审批弹窗', () => {
  it('在自定义弹窗内校验 Grant 时限，只提交 1–10 分钟的整数', async () => {
    const api = vi.fn(async (_path, options = {}) => (options.method ? {} : [approvalRecord()]))
    const wrapper = mount(ApprovalsView, { props: { api }, attachTo: document.body })
    await flushPromises()

    await findButton(wrapper, '批准').trigger('click')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('批准一次性授权')
    expect(wrapper.get('dialog[open] form').attributes()).toHaveProperty('novalidate')

    await wrapper.get('input[name="grant_minutes"]').setValue('2.5')
    findButton(wrapper, '确认批准').element.click()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('请输入 1 到 10 的整数分钟')
    expect(api.mock.calls.filter(([, options = {}]) => options.method === 'POST')).toHaveLength(0)
    expect(wrapper.find('dialog[open]').exists()).toBe(true)

    await wrapper.get('input[name="grant_minutes"]').setValue('7')
    findButton(wrapper, '确认批准').element.click()
    await flushPromises()

    const decisionCall = api.mock.calls.find(([path]) => path === '/v1/web/authorizations/request-1/decision')
    expect(decisionCall[1].method).toBe('POST')
    expect(JSON.parse(decisionCall[1].body)).toEqual({
      decision: 'APPROVED',
      grant_ttl_seconds: 420,
    })
    expect(wrapper.find('dialog[open]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('在审计详情区分待采集值和明确的零值', async () => {
    const summary = metricSummary({
      capture_status: 'ACTIVE',
      request_to_result_duration_ms: null,
      manual_handoff_count: 0,
      approval_followup_count: null,
      operation_review_duration_ms: 0,
      review_status: 'NOT_STARTED',
      can_record_manual_handoff: false,
      can_record_approval_followup: false,
      can_start_review: false,
      can_complete_review: false,
    })
    const api = vi.fn(async (path) => {
      if (path === '/v1/web/authorizations') return [approvalRecord()]
      if (path.endsWith('/audit')) return []
      if (path.endsWith('/pilot-metrics')) return summary
      throw new Error(`unexpected path ${path}`)
    })
    const wrapper = mount(ApprovalsView, { props: { api }, attachTo: document.body })
    await flushPromises()

    await findButton(wrapper, '审计').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="request-to-result-duration"]').text()).toBe('待采集')
    expect(wrapper.get('[data-testid="manual-handoff-count"]').text()).toBe('0 次')
    expect(wrapper.get('[data-testid="approval-followup-count"]').text()).toBe('待采集')
    expect(wrapper.get('[data-testid="operation-review-duration"]').text()).toBe('0 毫秒')
    expect(wrapper.get('[data-testid="improvement-target"]').text()).toBe('未预设')
    expect(wrapper.text()).toContain('ACTIVE')
    expect(wrapper.text()).toContain('NOT_STARTED')
    wrapper.unmount()
  })

  it('指标暂不可用时仍展示已加载的审计链', async () => {
    const api = vi.fn(async (path) => {
      if (path === '/v1/web/authorizations') return [approvalRecord()]
      if (path.endsWith('/audit')) return [{ id: 'audit-1', event_type: 'executions.insert', metadata: {}, created_at: '2026-07-16T12:00:00Z' }]
      if (path.endsWith('/pilot-metrics')) throw new Error('指标服务暂不可用')
      throw new Error(`unexpected path ${path}`)
    })
    const wrapper = mount(ApprovalsView, { props: { api }, attachTo: document.body })
    await flushPromises()

    await findButton(wrapper, '审计').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('executions.insert')
    expect(wrapper.get('[role="alert"]').text()).toContain('审计已加载，但试点指标暂不可用')
    wrapper.unmount()
  })

  it('通过确认弹窗记录计数，并直接记录复盘起止', async () => {
    const idempotencyKeys = ['key-handoff', 'key-followup', 'key-review-start', 'key-review-complete']
    const randomUUID = vi.fn()
    for (const key of idempotencyKeys) randomUUID.mockReturnValueOnce(key)
    vi.stubGlobal('crypto', { randomUUID })

    const summary = metricSummary()
    const api = vi.fn(async (path, options = {}) => {
      if (path === '/v1/web/authorizations') return [approvalRecord()]
      if (path.endsWith('/audit')) return []
      if (path.endsWith('/pilot-metrics')) return summary
      if (options.method === 'POST') return {}
      throw new Error(`unexpected path ${path}`)
    })
    const wrapper = mount(ApprovalsView, { props: { api }, attachTo: document.body })
    await flushPromises()
    await findButton(wrapper, '审计').trigger('click')
    await flushPromises()

    await findButton(wrapper, '记录人工转交').trigger('click')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('记录人工转交')
    findButton(wrapper, '确认记录转交').element.click()
    await flushPromises()

    await findButton(wrapper, '记录审批补问').trigger('click')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('记录审批补问')
    findButton(wrapper, '确认记录补问').element.click()
    await flushPromises()

    await findButton(wrapper, '开始复盘计时').trigger('click')
    await flushPromises()
    await findButton(wrapper, '完成复盘计时').trigger('click')
    await flushPromises()

    const posts = api.mock.calls.filter(([, options = {}]) => options.method === 'POST')
    expect(posts.map(([path]) => path)).toEqual([
      '/v1/web/authorizations/request-1/observations/manual-handoff',
      '/v1/web/authorizations/request-1/observations/approval-followup',
      '/v1/web/authorizations/request-1/reviews',
      '/v1/web/authorizations/request-1/reviews/review-1/complete',
    ])
    expect(posts.map(([, options]) => options.headers['Idempotency-Key'])).toEqual(idempotencyKeys)
    expect(posts.every(([, options]) => options.body === '{}')).toBe(true)
    expect(randomUUID).toHaveBeenCalledTimes(4)
    wrapper.unmount()
  })

  it('响应丢失后重试同一观测时复用幂等键', async () => {
    const randomUUID = vi.fn().mockReturnValue('stable-observation-key')
    vi.stubGlobal('crypto', { randomUUID })
    let postAttempts = 0
    const api = vi.fn(async (path, options = {}) => {
      if (path === '/v1/web/authorizations') return [approvalRecord()]
      if (path.endsWith('/audit')) return []
      if (path.endsWith('/pilot-metrics')) return metricSummary()
      if (options.method === 'POST') {
        postAttempts += 1
        if (postAttempts === 1) throw new Error('响应丢失')
        return {}
      }
      throw new Error(`unexpected path ${path}`)
    })
    const wrapper = mount(ApprovalsView, { props: { api }, attachTo: document.body })
    await flushPromises()
    await findButton(wrapper, '审计').trigger('click')
    await flushPromises()

    await findButton(wrapper, '记录人工转交').trigger('click')
    await flushPromises()
    findButton(wrapper, '确认记录转交').element.click()
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('响应丢失')

    findButton(wrapper, '确认记录转交').element.click()
    await flushPromises()

    const posts = api.mock.calls.filter(([, options = {}]) => options.method === 'POST')
    expect(posts).toHaveLength(2)
    expect(posts.map(([, options]) => options.headers['Idempotency-Key'])).toEqual([
      'stable-observation-key',
      'stable-observation-key',
    ])
    expect(randomUUID).toHaveBeenCalledTimes(1)
    expect(wrapper.find('dialog[open]').exists()).toBe(false)
    wrapper.unmount()
  })
})
