/**
 * @vitest-environment happy-dom
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AppInstallDialog from './AppInstallDialog.vue'
import source from './AppInstallDialog.vue?raw'
import { targetModeResolver, topologySelectField } from '../apps/registry/topology'

const servers = [
  { id: 'srv-1', name: '1', host: '192.168.74.132' },
  { id: 'srv-2', name: '2', host: '192.168.74.133' }
]

const app = {
  name: 'mysql',
  title: 'MySQL',
  icon: 'M',
  category: 'database' as const,
  categoryLabel: 'Database',
  sourceLabel: 'Bundle',
  description: 'Install MySQL',
  frontendReady: true as const,
  fallbackVersion: '8.0.36',
  installName: 'mysql',
  resourceApp: 'mysql',
  requiresServer: true,
  backendReady: true,
  versions: ['8.0.36'],
  resources: [],
  parts: {},
  deployable: true,
  missing: [],
  topologies: []
}

const topologies = [
  { name: 'standalone', label: '单体', targetMode: 'single' as const, default: true },
  { name: 'cluster', label: '集群', targetMode: 'multiple' as const, minTargets: 3 }
]

function mountDialog() {
  return mount(AppInstallDialog, {
    props: {
      modelValue: true,
      app,
      servers,
      targetMode: 'single',
      targetModeResolver: targetModeResolver(topologies),
      copy: {
        title: '安装 MySQL',
        versionLabel: '版本',
        versionPlaceholder: '选择版本',
        serversLabel: '目标服务器',
        serversPlaceholder: '选择服务器',
        noServers: '暂无服务器',
        selectedCount: (count: number) => `已选择 ${count} 台服务器`,
        cancel: '取消',
        submit: '开始安装'
      },
      fields: [topologySelectField('安装类型', topologies)]
    },
    global: {
      stubs: {
        ServerSelector: {
          props: ['modelValue', 'multiple', 'servers', 'placeholder'],
          template: '<div data-testid="server-selector">{{ multiple ? "multiple" : "single" }}</div>'
        },
        'el-dialog': { props: ['modelValue', 'title'], template: '<section><slot /><footer><slot name="footer" /></footer></section>' },
        'el-alert': { props: ['title'], template: '<div>{{ title }}</div>' },
        'el-form': { template: '<form><slot /></form>' },
        'el-form-item': { props: ['label'], template: '<label class="form-item"><span class="form-label">{{ label }}</span><slot /></label>' },
        'el-select': { template: '<div><slot /></div>' },
        'el-option': { props: ['label', 'value'], template: '<span>{{ label }}</span>' },
        'el-switch': { template: '<button type="button" />' },
        'el-input-number': { template: '<input />' },
        'el-input': { template: '<input />' },
        'el-button': { template: '<button type="button"><slot /></button>' }
      }
    }
  })
}

describe('AppInstallDialog layout', () => {
  it('shows install type before the server selector because topology controls target selection', () => {
    const wrapper = mountDialog()
    const labels = wrapper.findAll('.form-label').map((item) => item.text())

    expect(labels).toEqual(['版本', '安装类型', '目标服务器'])
    expect(wrapper.get('[data-testid="server-selector"]').text()).toBe('single')
  })

  it('allows long installation field labels to wrap inside the dialog instead of overflowing', () => {
    expect(source).not.toContain('white-space: nowrap')
    expect(source).toContain('white-space: normal')
    expect(source).toContain('overflow-wrap: anywhere')
  })
})
