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

function findButton(wrapper, label) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)
  expect(button, `missing button: ${label}`).toBeTruthy()
  return button
}

afterEach(() => {
  vi.restoreAllMocks()
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
})
