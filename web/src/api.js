const base = '/api'

async function req(method, url, body) {
  const res = await fetch(base + url, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.error || `HTTP ${res.status}`)
    err.issues = data.issues
    err.status = res.status
    throw err
  }
  return data
}

export const api = {
  health: () => req('GET', '/health'),
  createProject: (id, title) => req('POST', '/projects', { id, title }),
  getProject: (id) => req('GET', `/projects/${id}`),
  saveProject: (p) => req('PUT', `/projects/${p.id}`, p),
  importMedia: (pid, path) => req('POST', `/projects/${pid}/media/import`, { path }),
  validate: (pid, allowPlaceholder) =>
    req('POST', `/projects/${pid}/validate`, { allowPlaceholder }),
  export: (pid, allowPlaceholder) =>
    req('POST', `/projects/${pid}/export`, { allowPlaceholder }),
  mediaFileUrl: (pid, mid) => `${base}/projects/${pid}/media/${mid}/file`,
  thumbnailUrl: (pid, mid) => `${base}/projects/${pid}/media/${mid}/thumbnail`,
}
