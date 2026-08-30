'use client'

import Link from 'next/link'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {Card, CardContent, CardDescription, CardHeader} from '@/components/ui/card'
import {Alert, AlertDescription, AlertTitle} from '@/components/ui/alert'
import {Badge} from '@/components/ui/badge'
import {Separator} from '@/components/ui/separator'
import {cn} from '@/lib/utils'
import {KYCBasicForm} from '@/components/kyc-basic-form'
import {KYCSelfiePhoto} from '@/components/kyc-selfie-photo'
import {KYCDocumentUpload} from '@/components/kyc-document-upload'
import {CheckCircle2, FileSearch, Info, XCircle} from 'lucide-react'
import {fetchKYC, fetchPasskeys, fetchTOTPStatus} from '@/lib/queries'
import {submitEnhancedKYCAPI} from '@/lib/mutations'
import {formatDate} from '@/lib/format'
import {REQUIRED_DOC_TYPES, SUPPORT_EMAIL} from '@/lib/constants'
import type {KYCDocumentType, KYCStatus} from '@/lib/types'
import {toast} from 'sonner'
import {QueryError} from '@/components/query-error'

const READ_ONLY_LOCK_CLASS = 'read-only:bg-muted read-only:cursor-default'

const DOCUMENT_LABEL_KEY: Record<KYCDocumentType, string> = {
  id_front: 'identity.documentIdFront',
  id_back: 'identity.documentIdBack',
  selfie_with_document: 'identity.selfieWithDocument',
}

function StateBadge({status}: { status: KYCStatus }) {
  const {t} = useTranslation()
  switch (status.state) {
    case 'verified':
      return <Badge variant="default"><CheckCircle2 className="size-3.5"/>{t('identity.levelVerified')}</Badge>
    case 'basic_verified':
      return <Badge variant="secondary"><CheckCircle2 className="size-3.5"/>{t('identity.levelBasicVerified')}</Badge>
    case 'under_review':
      return <Badge variant="secondary"><FileSearch className="size-3.5"/>{t('identity.levelUnderReview')}</Badge>
    case 'rejected':
      return <Badge variant="destructive"><XCircle className="size-3.5"/>{t('identity.levelRejected')}</Badge>
    default:
      return <Badge variant="outline">{t('identity.levelNone')}</Badge>
  }
}

function ReadOnlyField({id, label, value}: { id: string; label: string; value: string }) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} readOnly className={cn('min-h-11', READ_ONLY_LOCK_CLASS)}/>
    </div>
  )
}

/** Read-only view shown once Basic data can no longer change (basic_verified onward). */
function SubmittedDetails({status}: { status: KYCStatus }) {
  const {t} = useTranslation()
  return (
    <div className="space-y-4">
      <ReadOnlyField id="submitted-cpf" label={t('identity.cpf')} value={status.cpf_masked ?? ''}/>
      <ReadOnlyField id="submitted-legal-name" label={t('identity.legalName')} value={status.legal_name ?? ''}/>
      <ReadOnlyField id="submitted-birth-date" label={t('identity.birthDate')}
                     value={formatDate(status.birth_date ?? '')}/>
      <ReadOnlyField id="submitted-phone" label={t('identity.phoneNumber')} value={status.phone_masked ?? ''}/>
    </div>
  )
}

function DocumentList({status}: { status: KYCStatus }) {
  const {t} = useTranslation()
  if (!status.documents?.length) return null
  return (
    <ul className="text-muted-foreground space-y-1 text-sm">
      {status.documents.map((doc) => (
        <li key={doc.id}>
          {t(DOCUMENT_LABEL_KEY[doc.type])} — {t('identity.uploadedOn', {date: formatDate(doc.uploaded_at)})}
        </li>
      ))}
    </ul>
  )
}

/** Every KYC write route sits behind step-up, which a user with no enrolled MFA method can never satisfy. */
function MFARequired() {
  const {t} = useTranslation()
  return (
    <Alert>
      <Info className="size-4"/>
      <AlertTitle>{t('identity.mfaRequiredTitle')}</AlertTitle>
      <AlertDescription className="space-y-3">
        <p>{t('identity.mfaRequired')}</p>
        <Button render={<Link href="/account/security"/>} className="min-h-11">{t('identity.mfaRequiredCta')}</Button>
      </AlertDescription>
    </Alert>
  )
}

/** Basic verified onward: upload the 3 Enhanced documents, then submit for review. */
function EnhancedSection({status}: { status: KYCStatus }) {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const uploadedTypes = status.documents?.map((d) => d.type) ?? []
  const docsComplete = REQUIRED_DOC_TYPES.every((docType) => uploadedTypes.includes(docType))

  const {mutate: submit, isPending, error} = useMutation({
    mutationFn: submitEnhancedKYCAPI,
    onSuccess: (st) => {
      queryClient.setQueryData(['kyc'], st)
      toast.success(t('identity.submittedOn', {date: formatDate(new Date().toISOString())}))
    },
  })

  return (
    <div className="space-y-4">
      {status.state === 'rejected' && (
        <Alert variant="destructive">
          <XCircle className="size-4"/>
          <AlertDescription className="space-y-2">
            {(status.rejection_code || status.rejection_reason) && (
              <p>{t('identity.rejectionReason', {
                reason: `${status.rejection_code ? t(`adminKyc.rejectionReasons.${status.rejection_code}`) : ''}${status.rejection_code && status.rejection_reason ? ' — ' : ''}${status.rejection_reason ?? ''}`,
              })}</p>
            )}
            <p>{t('identity.rejectionGuidance')}</p>
            <a href={`mailto:${SUPPORT_EMAIL}`}
               className="underline underline-offset-4">{t('identity.contactSupport')}</a>
          </AlertDescription>
        </Alert>
      )}

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{t('identity.submitFailed')}</AlertDescription>
        </Alert>
      )}

      <Alert>
        <Info className="size-4"/>
        <AlertDescription>{t('identity.documentsPrivacyNote')}</AlertDescription>
      </Alert>

      <DocumentList status={status}/>

      <div className="space-y-3">
        <h2 className="text-base font-semibold">{t('identity.documentIdFront')} / {t('identity.documentIdBack')}</h2>
        <KYCDocumentUpload uploadedTypes={uploadedTypes}/>
      </div>

      <Separator/>

      <div className="space-y-3">
        <h2 className="text-base font-semibold">{t('identity.selfieWithDocument')}</h2>
        <KYCSelfiePhoto uploaded={uploadedTypes.includes('selfie_with_document')}/>
      </div>

      <Button type="button" className="min-h-11" disabled={!docsComplete || isPending} onClick={() => submit()}>
        {isPending ? t('identity.submitting') : t('identity.submitEnhancedCta')}
      </Button>
    </div>
  )
}

function cardDescriptionKey(state: KYCStatus['state']): string {
  switch (state) {
    case 'basic_verified':
      return 'identity.basicVerifiedCta'
    case 'under_review':
      return 'identity.underReview'
    case 'rejected':
      return 'identity.rejected'
    case 'verified':
      return 'identity.levelVerified'
    default:
      return 'identity.notVerifiedCta'
  }
}

export default function IdentityPage() {
  const {t} = useTranslation()
  const {data: status, isError: kycError, error: kycErr, refetch: refetchKYC} = useQuery({
    queryKey: ['kyc'],
    queryFn: fetchKYC
  })
  const {data: totp, isError: totpError, error: totpErr, refetch: refetchTOTP} = useQuery({
    queryKey: ['totp'],
    queryFn: fetchTOTPStatus
  })
  const {
    data: passkeys,
    isError: passkeysError,
    error: passkeysErr,
    refetch: refetchPasskeys
  } = useQuery({queryKey: ['passkeys'], queryFn: fetchPasskeys})

  if (kycError || totpError || passkeysError) {
    return (
      <QueryError
        error={kycErr ?? totpErr ?? passkeysErr}
        onRetry={() => {
          void refetchKYC();
          void refetchTOTP();
          void refetchPasskeys()
        }}
      />
    )
  }

  const mfaLoaded = totp !== undefined && passkeys !== undefined
  const hasMFA = (totp?.enabled ?? false) || (passkeys?.length ?? 0) > 0

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">{t('identity.title')}</h1>
          <p className="text-muted-foreground text-sm mt-1">{t('identity.subtitle')}</p>
        </div>
        {status && <StateBadge status={status}/>}
      </div>

      <Card>
        <CardHeader>
          <CardDescription>{status ? t(cardDescriptionKey(status.state)) : null}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {status?.state === 'verified' && (
            <>
              <Alert>
                <CheckCircle2 className="size-4"/>
                <AlertDescription>
                  {t('identity.verifiedOn', {date: formatDate(status.verified_at ?? null)})} — {t('identity.lockedNote')}
                </AlertDescription>
              </Alert>
              <SubmittedDetails status={status}/>
            </>
          )}

          {status?.state === 'under_review' && (
            <>
              <SubmittedDetails status={status}/>
              <DocumentList status={status}/>
              <p className="text-muted-foreground text-sm">{t('identity.underReviewNote')}</p>
              {status.expires_at && (
                <p
                  className="text-muted-foreground text-sm">{t('identity.expiresOn', {date: formatDate(status.expires_at)})}</p>
              )}
            </>
          )}

          {status && (status.state === 'not_started' || status.state === 'basic_verified' || status.state === 'rejected') && (
            <>
              {mfaLoaded && !hasMFA ? (
                <MFARequired/>
              ) : status.state === 'not_started' ? (
                <KYCBasicForm status={status}/>
              ) : (
                <>
                  <SubmittedDetails status={status}/>
                  <Separator/>
                  <EnhancedSection status={status}/>
                </>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
