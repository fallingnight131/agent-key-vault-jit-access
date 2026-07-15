export function csrfFromCookie(cookie) {
  const item = cookie.split('; ').find((value) => value.startsWith('akv_csrf='))
  return item ? decodeURIComponent(item.split('=').slice(1).join('=')) : ''
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
    if (response.status === 401) {
      onUnauthorized()
      throw new Error('请重新登录')
    }
    if (!response.ok) {
      let body = {}
      try {
        body = await response.json()
      } catch {
        // The public fallback below intentionally hides non-JSON server bodies.
      }
      throw new Error(body.error || `请求失败 (${response.status})`)
    }
    return response.status === 204 ? null : response.json()
  }
}
