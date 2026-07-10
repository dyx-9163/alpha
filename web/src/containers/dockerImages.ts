export type DockerImageRow = {
  repository?: string
  tag?: string
  id?: string
}

export function imageReference(row: DockerImageRow) {
  const repository = String(row?.repository ?? '').trim()
  const tag = String(row?.tag ?? '').trim()
  const id = String(row?.id ?? '').trim()
  if (repository && repository !== '<none>' && tag && tag !== '<none>') {
    return `${repository}:${tag}`
  }
  return id
}

export function imageRowKey(row: DockerImageRow) {
  return imageReference(row) || `${String(row?.repository ?? '').trim()}:${String(row?.tag ?? '').trim()}:${String(row?.id ?? '').trim()}`
}

export function uniqueValues(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const next = value.trim()
    if (!next || seen.has(next)) continue
    seen.add(next)
    out.push(next)
  }
  return out
}
