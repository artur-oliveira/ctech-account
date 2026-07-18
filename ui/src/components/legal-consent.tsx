'use client'

import Link from 'next/link'
import { useTranslation } from 'react-i18next'
import { Label } from '@/components/ui/label'
import type { TermsPending } from '@/lib/types'

type LegalConsentProps = {
  /** Documents the server says this account still owes. */
  pending: TermsPending
  /** Documents the user has ticked so far. */
  accepted: TermsPending
  onChange: (accepted: TermsPending) => void
  disabled?: boolean
}

const CHECKBOX_CLASS = 'mt-0.5 size-4 shrink-0 rounded border-input accent-primary'
const DOC_LINK_CLASS = 'text-foreground underline underline-offset-4 hover:text-primary'

/**
 * Checkboxes for the legal documents a user must (re)accept. ToS and Privacy
 * version independently, so only the ones that actually moved are rendered —
 * asking someone to re-accept a document that never changed is noise.
 */
export function LegalConsent({ pending, accepted, onChange, disabled }: LegalConsentProps) {
  const { t } = useTranslation()

  return (
    <div className="space-y-3">
      {pending.tos && (
        <div className="flex items-start gap-2">
          <input
            id="accept_tos"
            type="checkbox"
            checked={accepted.tos}
            disabled={disabled}
            onChange={(e) => onChange({ ...accepted, tos: e.target.checked })}
            className={CHECKBOX_CLASS}
          />
          <Label htmlFor="accept_tos" className="text-sm font-normal text-muted-foreground">
            {t('register.acceptTermsPrefix')}{' '}
            <Link href="/terms" target="_blank" className={DOC_LINK_CLASS}>
              {t('register.termsOfService')}
            </Link>
            .
          </Label>
        </div>
      )}

      {pending.privacy && (
        <div className="flex items-start gap-2">
          <input
            id="accept_privacy"
            type="checkbox"
            checked={accepted.privacy}
            disabled={disabled}
            onChange={(e) => onChange({ ...accepted, privacy: e.target.checked })}
            className={CHECKBOX_CLASS}
          />
          <Label htmlFor="accept_privacy" className="text-sm font-normal text-muted-foreground">
            {t('register.acceptTermsPrefix')}{' '}
            <Link href="/privacy" target="_blank" className={DOC_LINK_CLASS}>
              {t('register.privacyPolicy')}
            </Link>
            .
          </Label>
        </div>
      )}
    </div>
  )
}

/** True once every pending document has been ticked. */
export function isConsentComplete(pending: TermsPending, accepted: TermsPending): boolean {
  return (!pending.tos || accepted.tos) && (!pending.privacy || accepted.privacy)
}

export const NOTHING_ACCEPTED: TermsPending = { tos: false, privacy: false }
