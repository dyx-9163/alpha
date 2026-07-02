export const permissions = {
  settingsManage: 'settings.manage',
  usersManage: 'users.manage',
  resourcesScan: 'resources.scan',
  serversManage: 'servers.manage',
  terminalConnect: 'terminal.connect',
  tasksManage: 'tasks.manage',
  auditManage: 'audit.manage',
  appsManage: 'apps.manage',
  containersManage: 'containers.manage',
  databaseManage: 'database.manage',
  storageManage: 'storage.manage',
  credentialsUse: 'credentials.use',
  credentialsManage: 'credentials.manage'
} as const

export type Permission = typeof permissions[keyof typeof permissions]

const rolePermissions: Record<string, Permission[]> = {
  owner: Object.values(permissions),
  admin: Object.values(permissions),
  operator: [
    permissions.resourcesScan,
    permissions.serversManage,
    permissions.terminalConnect,
    permissions.tasksManage,
    permissions.appsManage,
    permissions.containersManage,
    permissions.databaseManage,
    permissions.storageManage,
    permissions.credentialsUse
  ],
  viewer: [],
  auditor: []
}

export function permissionsForRole(role: string) {
  return rolePermissions[role.trim().toLowerCase()]?.slice() ?? []
}

export function normalizePermissions(role: string, granted?: string[]) {
  const values = [
    ...permissionsForRole(role),
    ...(Array.isArray(granted) ? granted : [])
  ]
  return Array.from(new Set(values.filter((value): value is Permission => isPermission(value))))
}

export function isPermission(value: string): value is Permission {
  return (Object.values(permissions) as string[]).includes(value)
}
