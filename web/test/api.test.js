import { describe, expect, it, vi } from 'vitest'
import { createAPI, csrfFromCookie } from '../src/api.js'
import { encodeBase64, roleLabel } from '../src/helpers.js'

function response(status, body = {}) {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: vi.fn().mockResolvedValue(body),
  }
}

describe('AKV API client', () => {
  it('reads and decodes the CSRF cookie', () => {
    expect(csrfFromCookie('other=x; akv_csrf=a%2Bb%3D')).toBe('a+b=')
  })

  it('sends same-origin credentials and CSRF only for mutations', async () => {
    const fetchImpl = vi.fn().mockResolvedValue(response(204))
    const api = createAPI({ fetchImpl, cookieSource: () => 'akv_csrf=proof' })

    await api('/read')
    await api('/write', { method: 'POST', body: '{}' })

    expect(fetchImpl.mock.calls[0][1].credentials).toBe('same-origin')
    expect(fetchImpl.mock.calls[0][1].headers['X-AKV-CSRF']).toBeUndefined()
    expect(fetchImpl.mock.calls[1][1].headers['X-AKV-CSRF']).toBe('proof')
  })

  it('returns to authentication state on 401', async () => {
    const onUnauthorized = vi.fn()
    const api = createAPI({
      fetchImpl: vi.fn().mockResolvedValue(response(401, { error: 'UNAUTHORIZED' })),
      onUnauthorized,
    })

    await expect(api('/private')).rejects.toThrow('请重新登录')
    expect(onUnauthorized).toHaveBeenCalledOnce()
  })

  it.each([
    [401, 'INVALID_CREDENTIALS', '用户名或密码错误'],
    [400, 'INVALID_REGISTRATION', '用户名或密码不符合注册要求'],
    [503, 'REGISTRATION_UNAVAILABLE', '系统尚未初始化，请先创建管理员账号'],
    [409, 'USERNAME_UNAVAILABLE', '用户名已被使用，请换一个'],
    [403, 'FORBIDDEN', '没有权限执行此操作'],
    [403, 'CSRF_REJECTED', '页面校验已过期，请刷新后重试'],
    [400, 'INVALID_OBSERVATION', '观测记录格式无效，请刷新后重试'],
    [409, 'OBSERVATION_REJECTED', '当前状态不能记录这项观测，请刷新后重试'],
    [404, 'NOT_FOUND', '申请不存在或你无权查看'],
    [500, 'INTERNAL', '服务暂时不可用，请稍后重试'],
  ])('translates public API error %s', async (status, code, message) => {
    const api = createAPI({ fetchImpl: vi.fn().mockResolvedValue(response(status, { error: code })) })

    await expect(api('/v1/web/register', { method: 'POST', body: '{}' })).rejects.toThrow(message)
  })
})

describe('display helpers', () => {
  it('encodes Unicode secret input as UTF-8 base64', () => {
    expect(encodeBase64('测试')).toBe('5rWL6K+V')
  })

  it('keeps role labels distinct', () => {
    expect(roleLabel({ is_admin: true, approve_all: true })).toBe('唯一管理员')
    expect(roleLabel({ is_admin: false, approve_all: true })).toBe('全局审批人')
    expect(roleLabel({ is_admin: false, approve_all: false })).toBe('Agent 所有者')
  })
})
