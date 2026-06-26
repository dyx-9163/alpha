export type ServerStatus = 'available' | 'unknown' | 'failed' | 'running' | string

export type AuthType = 'password' | 'privateKey' | string

export interface ServerRecord {
  id: string
  name: string
  host: string
  port: number
  username: string
  authType: AuthType
  tags?: string
  note?: string
  deployDir?: string
  status?: ServerStatus
  lastError?: string
  createdAt?: string
  updatedAt?: string
  password?: string
  privateKey?: string
}

export interface ServerFormModel {
  id?: string
  name: string
  host: string
  port: number
  username: string
  authType: AuthType
  password?: string
  privateKey?: string
  tags?: string
  note?: string
  deployDir: string
  status?: ServerStatus
}

export interface ProbeTaskResponse {
  taskId: string
  status?: string
}
