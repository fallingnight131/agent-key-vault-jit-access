export function csrfFromCookie(cookie) {
  const item = cookie.split('; ').find((value) => value.startsWith('akv_csrf='))
  return item ? decodeURIComponent(item.split('=').slice(1).join('=')) : ''
}

const publicErrorMessages = {
  INVALID_CREDENTIALS: '用户名或密码错误',
  INVALID_REGISTRATION: '用户名或密码不符合注册要求',
  REGISTRATION_UNAVAILABLE: '系统尚未初始化，请先创建管理员账号',
  USERNAME_UNAVAILABLE: '用户名已被使用，请换一个',
  FORBIDDEN: '没有权限执行此操作',
  CSRF_REJECTED: '页面校验已过期，请刷新后重试',
  INTERNAL: '服务暂时不可用，请稍后重试',
}

export function createAPI({
  fetchImpl = globalThis.fetch.bind(globalThis),
  cookieSource = () => document.cookie,
  onUnauthorized = () => {},
} = {}) {
  return async function api(path, options = {}) {
    const method = (options.method || 'GET').toUpperCase()
    const headers = {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    }
    if (method !== 'GET' && method !== 'HEAD') {
      headers['X-AKV-CSRF'] = csrfFromCookie(cookieSource())
    }

    const response = await fetchImpl(path, {
      credentials: 'same-origin',
      ...options,
      method,
      headers,
    })
    if (!response.ok) {
      let body = {}
      try {
        body = await response.json()
      } catch {
        // The public fallback below intentionally hides non-JSON server bodies.
      }
      if (response.status === 401) onUnauthorized()
      const fallback = response.status === 401
        ? '请重新登录'
        : (body.error || `请求失败 (${response.status})`)
      throw new Error(publicErrorMessages[body.error] || fallback)
    }
    return response.status === 204 ? null : response.json()
  }
}
