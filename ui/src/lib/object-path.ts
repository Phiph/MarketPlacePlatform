// Immutable set-by-path for building a nested spec object out of a flat set
// of form fields. Clones only the objects along `path`, so sibling branches
// keep their identity (fine here since nothing memoizes on them).
export function setPath(obj: Record<string, unknown>, path: string[], value: unknown): Record<string, unknown> {
  if (path.length === 0) return obj
  const [head, ...rest] = path
  const clone = { ...obj }
  if (rest.length === 0) {
    clone[head] = value
  } else {
    const child = (clone[head] as Record<string, unknown>) ?? {}
    clone[head] = setPath(child, rest, value)
  }
  return clone
}

export function getPath(obj: Record<string, unknown> | undefined, path: string[]): unknown {
  let cur: unknown = obj
  for (const segment of path) {
    if (cur == null || typeof cur !== 'object') return undefined
    cur = (cur as Record<string, unknown>)[segment]
  }
  return cur
}
