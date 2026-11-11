import { afterEach, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { checkUpdates } from '@/api/admin/system'
import { useAppStore } from '@/stores/app'
import VersionBadge from '../VersionBadge.vue'

vi.mock('@/api/admin/system', () => ({
  checkUpdates: vi.fn(),
  performUpdate: vi.fn(),
  restartService: vi.fn(),
  getRollbackVersions: vi.fn(),
  rollback: vi.fn()
}))
vi.mock('@/api/auth', () => ({ getPublicSettings: vi.fn() }))
vi.mock('@/i18n', () => ({ i18n: { global: { t: (key: string) => key } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores', async () => ({
  useAppStore: (await import('@/stores/app')).useAppStore,
  useAuthStore: () => ({ isAdmin: true })
}))

enableAutoUnmount(afterEach)

it('links a fork container to its GHCR package through the cached version response', async () => {
  const packageUrl = 'https://github.com/nbb2025/sub2api/pkgs/container/sub2api'
  vi.mocked(checkUpdates).mockResolvedValueOnce({
    current_version: '0.2.20-nbb.f6744f79ad5b',
    latest_version: '',
    has_update: false,
    build_type: 'release',
    cached: false,
    manual_update_url: packageUrl
  })
  const pinia = createPinia()
  const wrapper = mount(VersionBadge, { global: { plugins: [pinia], stubs: { Icon: true } } })
  await flushPromises()
  expect(await useAppStore(pinia).fetchVersion()).toMatchObject({
    manual_update_url: packageUrl,
    cached: true
  })
  expect(checkUpdates).toHaveBeenCalledTimes(1)

  await wrapper.get('button').trigger('click')
  const link = wrapper.get('a')
  expect(link.text()).toBe('version.manualUpdate')
  expect(link.attributes('href')).toBe(packageUrl)
  expect(wrapper.findAll('button')).toHaveLength(1)
  expect(wrapper.text()).not.toContain('version.upToDate')
})
