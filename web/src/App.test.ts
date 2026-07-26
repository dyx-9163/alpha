import { describe, expect, it } from 'vitest'

import source from './App.vue?raw'

describe('private route keep-alive contract', () => {
  it('caches only the named terminal view inside the authenticated layout', () => {
    expect(source).toContain('<router-view v-slot="{ Component }">')
    expect(source).toContain('<keep-alive include="TerminalView">')
    expect(source).toContain('<component :is="Component" />')
    expect(source).toContain('<router-view v-if="$route.meta.public" />')
  })
})
