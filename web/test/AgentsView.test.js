import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AgentsView from '../src/views/AgentsView.vue'

function findButton(wrapper, label) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text() === label)
  expect(button, `missing button: ${label}`).toBeTruthy()
  return button
}

function record(overrides = {}) {
  return {
    id: 'agent-1',
    name: '排障 Agent',
    active: true,
    has_active_token: true,
    token_expires_at: null,
    ...overrides,
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('Agent Token 管理', () => {
  it('区分永久有效和已撤销 Token，且不为已撤销 Token 显示撤销按钮', async () => {
    const api = vi.fn(async () => [
      record(),
      record({ id: 'agent-2', name: '已撤销 Agent', has_active_token: false }),
    ])
    const wrapper = mount(AgentsView, { props: { api } })
    await flushPromises()

    expect(wrapper.text()).toContain('Token 永久有效')
    expect(wrapper.text()).toContain('Token 已撤销')
    expect(wrapper.findAll('button').filter((button) => button.text() === '撤销 Token')).toHaveLength(1)
    wrapper.unmount()
  })

  it('撤销前显示自定义确认弹窗，取消不发请求，确认后更新状态', async () => {
    let hasActiveToken = true
    const api = vi.fn(async (path, options = {}) => {
      if (path === '/v1/web/agents' && !options.method) {
        return [record({ has_active_token: hasActiveToken })]
      }
      if (path === '/v1/web/agents/agent-1/token' && options.method === 'DELETE') {
        hasActiveToken = false
        return {}
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const wrapper = mount(AgentsView, { props: { api } })
    await flushPromises()

    await findButton(wrapper, '撤销 Token').trigger('click')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain('撤销 Agent Token')
    expect(api.mock.calls.filter(([, options = {}]) => options.method === 'DELETE')).toHaveLength(0)

    await findButton(wrapper, '取消').trigger('click')
    await flushPromises()
    expect(wrapper.find('dialog[open]').exists()).toBe(false)
    expect(api.mock.calls.filter(([, options = {}]) => options.method === 'DELETE')).toHaveLength(0)

    await findButton(wrapper, '撤销 Token').trigger('click')
    await flushPromises()
    expect(findButton(wrapper, '确认撤销').attributes('type')).toBe('submit')
    await wrapper.get('dialog[open] form').trigger('submit')
    await flushPromises()

    const revokeCall = api.mock.calls.find(([path]) => path === '/v1/web/agents/agent-1/token')
    expect(revokeCall[1]).toEqual({ method: 'DELETE' })
    expect(wrapper.text()).toContain('排障 Agent 的 Token 已撤销')
    expect(wrapper.findAll('button').some((button) => button.text() === '撤销 Token')).toBe(false)
    expect(wrapper.find('dialog[open]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('注册后将焦点移到一次性 Token 弹窗，并且只能确认保存后关闭', async () => {
    const oneTimeToken = 'fake-one-time-token'
    let agents = []
    const api = vi.fn(async (path, options = {}) => {
      if (path === '/v1/web/agents' && !options.method) return agents.map((agent) => ({ ...agent }))
      if (path === '/v1/web/agents' && options.method === 'POST') {
        agents = [record()]
        return { token: oneTimeToken }
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const wrapper = mount(AgentsView, { props: { api }, attachTo: document.body })
    await flushPromises()

    await findButton(wrapper, '注册 Agent').trigger('click')
    await wrapper.get('input[name="agent_name"]').setValue('排障 Agent')
    findButton(wrapper, '注册并生成 Token').element.click()
    await flushPromises()

    const tokenDialog = wrapper.get('dialog[open]')
    expect(tokenDialog.text()).toContain(oneTimeToken)
    expect(tokenDialog.find('.modal-close').exists()).toBe(false)
    expect(tokenDialog.findAll('button').some((button) => button.text() === '取消')).toBe(false)
    expect(document.activeElement).toBe(tokenDialog.get('h2').element)

    await tokenDialog.trigger('cancel')
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain(oneTimeToken)

    wrapper.get('dialog[open]').element.click()
    await flushPromises()
    expect(wrapper.get('dialog[open]').text()).toContain(oneTimeToken)

    findButton(wrapper, '我已安全保存').element.click()
    await flushPromises()
    expect(wrapper.find('dialog[open]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(oneTimeToken)
    wrapper.unmount()
  })

  it('轮换后也将焦点移到新 Token 弹窗', async () => {
    const api = vi.fn(async (path, options = {}) => {
      if (path === '/v1/web/agents' && !options.method) return [record()]
      if (path === '/v1/web/agents/agent-1/rotate-token' && options.method === 'POST') {
        return { token: 'fake-rotated-token' }
      }
      throw new Error(`unexpected request: ${path}`)
    })
    const wrapper = mount(AgentsView, { props: { api }, attachTo: document.body })
    await flushPromises()

    await findButton(wrapper, '轮换 Token').trigger('click')
    await flushPromises()
    findButton(wrapper, '确认轮换').element.click()
    await flushPromises()

    const tokenDialog = wrapper.get('dialog[open]')
    expect(tokenDialog.text()).toContain('fake-rotated-token')
    expect(document.activeElement).toBe(tokenDialog.get('h2').element)
    findButton(wrapper, '我已安全保存').element.click()
    await flushPromises()
    wrapper.unmount()
  })
})
