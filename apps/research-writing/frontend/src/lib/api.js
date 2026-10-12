const base = new URL('./', window.location.href)
export async function api(path, body) {
  const response = await fetch(new URL('api/' + path, base), {
    method: body === undefined ? 'GET' : 'POST',
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
  })
  let result
  try { result = await response.json() } catch {
    throw new Error(response.status === 401 || response.status === 403 ? '访问已过期，请在微信获取长期 key 后重新进入。' : '服务暂时不可用，请稍后重试。')
  }
  if (!response.ok) {
    const error = new Error(result.error || '请求失败，请重试')
    error.status = response.status; error.current = result.current
    throw error
  }
  return result
}
export function operationID() {
  return crypto.randomUUID()
}
export function download(data, filename) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
  const a = document.createElement('a'); a.href = url; a.download = filename; a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
