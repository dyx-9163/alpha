import type { AppFrontendModule } from './contract'

const discovered = import.meta.glob<{ default: AppFrontendModule }>('../*/index.ts', { eager: true })
const modules: AppFrontendModule[] = Object.values(discovered)
  .map((entry) => entry.default)
  .filter((module): module is AppFrontendModule => Boolean(module?.name && module.manifest))
  .sort((a, b) => a.name.localeCompare(b.name))

export function registeredFrontendModules() {
  return modules
}

export function frontendModuleFor(name: string) {
  return modules.find((module) => module.name === name) ?? null
}

export function frontendAppCatalog(locale?: string) {
  return modules.map((module) => module.manifest(locale))
}
