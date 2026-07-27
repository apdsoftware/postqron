import type { AdminMessageKey } from '../core/catalogs.ts'

export interface AdminNavItem {
  path: string
  labelKey: AdminMessageKey
  icon: string
}

export const ADMIN_NAV_ITEMS: readonly AdminNavItem[] = [
  { path: '/admin', labelKey: 'nav.dashboard', icon: '◧' },
  { path: '/admin/users', labelKey: 'nav.users', icon: '◔' },
  { path: '/admin/workspaces', labelKey: 'nav.workspaces', icon: '▤' },
  { path: '/admin/plans', labelKey: 'nav.plans', icon: '◈' },
  { path: '/admin/audit', labelKey: 'nav.audit', icon: '≣' },
  { path: '/admin/profile', labelKey: 'nav.profile', icon: '◑' },
]

export function adminSectionPath(routePath: string): string {
  const segments = routePath.split('/').filter(Boolean)
  const adminIndex = segments.indexOf('admin')
  if (adminIndex === -1) {
    return '/admin'
  }
  const section = segments[adminIndex + 1]
  return section ? `/admin/${section}` : '/admin'
}

export function isAdminNavItemActive(item: AdminNavItem, routePath: string): boolean {
  return adminSectionPath(routePath) === item.path
}
