import type { User } from './types'

const SUPPORT_ROLE_RANK: Record<User['support_role'], number> = {
  '': 0,
  agent: 1,
  manager: 2,
  admin: 3,
}

/** UI-only affordance helper. Backend middleware remains the authority. */
export function hasSupportRole(role: User['support_role'], minimum: Exclude<User['support_role'], ''>): boolean {
  return SUPPORT_ROLE_RANK[role] >= SUPPORT_ROLE_RANK[minimum]
}
