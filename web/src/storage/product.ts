export function objectStorageProduct(metadata?: string | Record<string, unknown>): 'MinIO' | 'Silo' {
  try {
    const data = typeof metadata === 'string' ? JSON.parse(metadata) : metadata
    return typeof data?.serverProduct === 'string' && data.serverProduct.trim().toLowerCase() === 'silo' ? 'Silo' : 'MinIO'
  } catch {
    return 'MinIO'
  }
}
