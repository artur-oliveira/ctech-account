'use client'

import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import type { OrganizationRole } from '@/lib/types'

/**
 * One vocabulary for the role, wherever it appears. Owner is the only one that
 * carries the accent — it is the role with the powers nobody else has, and
 * tinting all four would spend the accent on rank rather than on meaning
 * (DESIGN.md §2: cobalt on ≤10% of a screen).
 */
export function OrganizationRoleBadge({ role }: { role: OrganizationRole }) {
  const { t } = useTranslation()
  return (
    <Badge variant={role === 'owner' ? 'default' : 'secondary'} className="text-xs">
      {t(`organizations.roles.${role}`)}
    </Badge>
  )
}
