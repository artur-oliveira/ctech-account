'use client'

import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import type { OrganizationKind } from './types'

/**
 * The key to render for a workspace of this kind. A space reads
 * `spaces.<rest>` where one exists — `organizations.settings.transfer` becomes
 * `spaces.settings.transfer`, `toast.transferFailed` becomes
 * `spaces.toast.transferFailed` — and the shared key where nothing differs
 * ("E-mail", "Expira em"). An organization is never touched.
 */
export function workspaceKey(
  key: string,
  kind: OrganizationKind | undefined,
  exists: (key: string) => boolean,
): string {
  if (kind !== 'personal') return key
  const spaceKey = `spaces.${key.replace(/^organizations\./, '')}`
  return exists(spaceKey) ? spaceKey : key
}

/**
 * `t` for a workspace: the same components render an organization and a space,
 * and a space must never be called an organization or offered a company.
 */
export function useWorkspaceT(kind: OrganizationKind | undefined) {
  const { t, i18n } = useTranslation()
  return useCallback(
    (key: string, options?: Record<string, unknown>) =>
      t(workspaceKey(key, kind, (k) => i18n.exists(k)), options),
    [kind, t, i18n],
  )
}

/**
 * The server's problem detail, or nothing on a space. The API's details are
 * written for organizations ("You do not have access to this organization."),
 * so on a space the caller falls back to its own `spaces.*` copy instead of
 * passing that text to the screen.
 */
export function workspaceDetail(detail: string | undefined, kind: OrganizationKind | undefined): string | undefined {
  return kind === 'personal' ? undefined : detail
}
