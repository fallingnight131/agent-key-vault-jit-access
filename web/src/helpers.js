export function formatDate(value) {
  return value ? new Date(value).toLocaleString() : '—'
}

export function prettyJSON(value) {
  return JSON.stringify(value, null, 2)
}

export function roleLabel(user) {
  if (user.is_admin) return '唯一管理员'
  if (user.approve_all) return '全局审批人'
  return 'Agent 所有者'
}

export function encodeBase64(value) {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}
