import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

function loadDefaultsEnv() {
  const file = path.join(rootDir, 'config', 'defaults.env')
  if (!existsSync(file)) {
    return {}
  }
  return readFileSync(file, 'utf8').split(/\r?\n/).reduce<Record<string, string>>((env, line) => {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) {
      return env
    }
    const index = trimmed.indexOf('=')
    if (index <= 0) {
      return env
    }
    env[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
    return env
  }, {})
}

function apiProxyTarget() {
  const defaults = loadDefaultsEnv()
  const addr = (process.env.AIFAR_ADDR || defaults.AIFAR_ADDR || '0.0.0.0:8080').trim()
  const target = addr.startsWith('http://') || addr.startsWith('https://') ? addr : `http://${addr}`
  const url = new URL(target)
  if (url.hostname === '0.0.0.0' || url.hostname === '::') {
    url.hostname = '127.0.0.1'
  }
  return url.toString().replace(/\/$/, '')
}

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api/v2': {
        target: apiProxyTarget(),
        changeOrigin: true,
        ws: true
      }
    }
  }
})
