export async function keepPreviousArrayOnLoadFailure<T>(request: Promise<T[] | null | undefined>, previous: T[]) {
  try {
    const value = await request
    return Array.isArray(value) ? value : []
  } catch {
    return previous
  }
}

export async function keepPreviousObjectOnLoadFailure<T extends Record<string, unknown>>(request: Promise<T | null | undefined>, previous: T) {
  try {
    return (await request) ?? previous
  } catch {
    return previous
  }
}
