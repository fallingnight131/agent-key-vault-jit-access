import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import App from '../src/App.vue'

const admin = {
  id: 'user-1',
  username: 'admin',
  is_admin: true,
  approve_all: false,
  owner_active: true,
}

const ordinaryUser = {
  id: 'user-2',
  username: 'new-user',
  is_admin: false,
  approve_all: false,
  owner_active: true,
}

function response(status, body = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: vi.fn().mockResolvedValue(body),
  }
}

afterEach(() => {
  document.cookie = 'akv_csrf=; Max-Age=0; Path=/'
  vi.unstubAllGlobals()
})

describe('authentication view transition', () => {
  it('shows login after an anonymous boot and replaces it with the workspace after login', async () => {
    let authenticated = false
    vi.stubGlobal('fetch', vi.fn(async (path) => {
      if (path === '/v1/web/login') {
        authenticated = true
        document.cookie = 'akv_csrf=proof; Path=/'
        return response(200, admin)
      }
      if (path === '/v1/web/me') return authenticated ? response(200, admin) : response(401)
      if (path === '/v1/web/authorizations') return response(200, [])
      return response(404, { error: 'NOT_FOUND' })
    }))

    const wrapper = mount(App)
    await flushPromises()
    expect(wrapper.find('.login-shell').exists()).toBe(true)
    expect(wrapper.find('.app-shell').exists()).toBe(false)

    await wrapper.get('input[name="username"]').setValue('admin')
    await wrapper.get('input[name="password"]').setValue('password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('.login-shell').exists()).toBe(false)
    expect(wrapper.find('.app-shell').exists()).toBe(true)
    expect(wrapper.text()).toContain('唯一管理员')
    expect(wrapper.text()).toContain('暂无授权申请')
    wrapper.unmount()
  })

  it('switches between login and registration forms', async () => {
    vi.stubGlobal('fetch', vi.fn(async (path) => {
      if (path === '/v1/web/me') return response(401, { error: 'UNAUTHORIZED' })
      return response(404, { error: 'NOT_FOUND' })
    }))

    const wrapper = mount(App)
    await flushPromises()

    expect(wrapper.get('button.auth-switch').text()).toBe('注册账号')
    expect(wrapper.find('input[name="password_confirmation"]').exists()).toBe(false)

    await wrapper.get('button.auth-switch').trigger('click')
    expect(wrapper.get('button[type="submit"]').text()).toBe('创建账号')
    expect(wrapper.get('input[name="password"]').attributes('autocomplete')).toBe('new-password')
    expect(wrapper.find('input[name="password_confirmation"]').exists()).toBe(true)

    await wrapper.get('button.auth-switch').trigger('click')
    expect(wrapper.get('button[type="submit"]').text()).toBe('进入控制台')
    expect(wrapper.get('input[name="password"]').attributes('autocomplete')).toBe('current-password')
    expect(wrapper.find('input[name="password_confirmation"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not request registration when the passwords differ', async () => {
    const fetchMock = vi.fn(async (path) => {
      if (path === '/v1/web/me') return response(401, { error: 'UNAUTHORIZED' })
      return response(404, { error: 'NOT_FOUND' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('button.auth-switch').trigger('click')
    await wrapper.get('input[name="username"]').setValue('new-user')
    await wrapper.get('input[name="password"]').setValue('first-password')
    await wrapper.get('input[name="password_confirmation"]').setValue('second-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(fetchMock.mock.calls.some(([path]) => path === '/v1/web/register')).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toBe('两次输入的密码不一致')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('registers a basic user and enters the workspace immediately', async () => {
    const fetchMock = vi.fn(async (path) => {
      if (path === '/v1/web/me') return response(401, { error: 'UNAUTHORIZED' })
      if (path === '/v1/web/register') {
        document.cookie = 'akv_csrf=proof; Path=/'
        return response(201, ordinaryUser)
      }
      if (path === '/v1/web/authorizations') return response(200, [])
      return response(404, { error: 'NOT_FOUND' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('button.auth-switch').trigger('click')
    await wrapper.get('input[name="username"]').setValue('new-user')
    await wrapper.get('input[name="password"]').setValue('safe-password')
    await wrapper.get('input[name="password_confirmation"]').setValue('safe-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    const [, options] = fetchMock.mock.calls.find(([path]) => path === '/v1/web/register')
    expect(options.method).toBe('POST')
    expect(JSON.parse(options.body)).toEqual({ username: 'new-user', password: 'safe-password' })
    expect(wrapper.find('.login-shell').exists()).toBe(false)
    expect(wrapper.find('.app-shell').exists()).toBe(true)
    expect(wrapper.text()).toContain('Agent 所有者')
    expect(wrapper.text()).toContain('暂无授权申请')
    expect(wrapper.findAll('nav button').map((button) => button.text())).toEqual(['待审批', 'Agent'])
    wrapper.unmount()
  })

  it('keeps the registration form open when the username is unavailable', async () => {
    const fetchMock = vi.fn(async (path) => {
      if (path === '/v1/web/me') return response(401, { error: 'UNAUTHORIZED' })
      if (path === '/v1/web/register') return response(409, { error: 'USERNAME_UNAVAILABLE' })
      return response(404, { error: 'NOT_FOUND' })
    })
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('button.auth-switch').trigger('click')
    await wrapper.get('input[name="username"]').setValue('existing-user')
    await wrapper.get('input[name="password"]').setValue('safe-password')
    await wrapper.get('input[name="password_confirmation"]').setValue('safe-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('.login-shell').exists()).toBe(true)
    expect(wrapper.find('input[name="password_confirmation"]').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toBe('用户名已被使用，请换一个')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('keeps registration retryable when the system is not initialized', async () => {
    vi.stubGlobal('fetch', vi.fn(async (path) => {
      if (path === '/v1/web/me') return response(401, { error: 'UNAUTHORIZED' })
      if (path === '/v1/web/register') return response(503, { error: 'REGISTRATION_UNAVAILABLE' })
      return response(404, { error: 'NOT_FOUND' })
    }))

    const wrapper = mount(App)
    await flushPromises()
    await wrapper.get('button.auth-switch').trigger('click')
    await wrapper.get('input[name="username"]').setValue('new-user')
    await wrapper.get('input[name="password"]').setValue('safe-password')
    await wrapper.get('input[name="password_confirmation"]').setValue('safe-password')
    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('input[name="password_confirmation"]').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toBe('系统尚未初始化，请先创建管理员账号')
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
