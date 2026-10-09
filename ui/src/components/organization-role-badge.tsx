'use client'

import { Badge } from '@/components/ui/badge'
import { useWorkspaceT } from '@/lib/workspace-copy'
import type { OrganizationKind, OrganizationRole } from '@/lib/types'

/**
 * One vocabulary for the role, wherever it appears. Owner is the only one that
 * carries the accent — it is the role with the powers nobody else has, and
 * tinting all four would spend the accent on rank rather than on meaning
 * (DESIGN.md §2: cobalt on ≤10% of a screen).
 *
 * A space names the same roles differently — Dono, Acesso total, Leitura — so
 * the kind travels with the role.
 */
export function OrganizationRoleBadge({
  role,
  kind,
}: {
  role: OrganizationRole
  kind?: OrganizationKind
}) {
  const wt = useWorkspaceT(kind)
  return (
    <Badge variant={role === 'owner' ? 'default' : 'secondary'} className="text-xs">
      {wt(`organizations.roles.${role}`)}
    </Badge>
  )
}
