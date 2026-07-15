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

function response(status, body = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: vi.fn().mockResolvedValue(body),
  }
}

afterEach(() => {
  document.cookie = 'akv_csrf=; Max-Age=0; Path=/'
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
})
