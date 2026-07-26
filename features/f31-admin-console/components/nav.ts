import type { AdminMessageKey } from '../core/catalogs.ts'

export interface AdminNavItem {
  readonly icon: string
  readonly labelKey: AdminMessageKey
  readonly path: string
}

export const ADMIN_NAV_ITEMS: readonly AdminNavItem[] = [
  { path: '/admin', labelKey: 'nav.dashboard', icon: '⌂' },
  { path: '/admin/users', labelKey: 'nav.users', icon: '◔' },
  { path: '/admin/workspaces', labelKey: 'nav.workspaces', icon: '▦' },
  { path: '/admin/plans', labelKey: 'nav.plans', icon: '◈' },
  { path: '/admin/audit', labelKey: 'nav.audit', icon: '≡' },
  { path: '/admin/profile', labelKey: 'nav.profile', icon: '●' },
]
