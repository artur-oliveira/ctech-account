'use client'

import { ChangeEvent, useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import Image from 'next/image'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { uploadKYCDocumentAPI } from '@/lib/mutations'
import { ID_DOCUMENT_ACCEPTED_TYPES, ID_DOCUMENT_PREVIEWABLE_TYPES, MAX_DOCUMENT_BYTES } from '@/lib/constants'
import type { KYCDocumentType } from '@/lib/types'
import { FileText, Upload } from 'lucide-react'

// The four selfie poses are recorded via <SelfieCapture/> (camera + short
// clips), not this file picker — only the two static ID photos go through it.
const DOCUMENT_TYPES: KYCDocumentType[] = ['id_front', 'id_back']

const TYPE_LABEL_KEY: Record<KYCDocumentType, string> = {
  id_front: 'identity.documentIdFront',
  id_back: 'identity.documentIdBack',
  selfie_up: 'identity.selfieUp',
  selfie_down: 'identity.selfieDown',
  selfie_left: 'identity.selfieLeft',
  selfie_right: 'identity.selfieRight',
}

/**
 * Uploads one identity document: pick a type, pick a file, PUT it straight to
 * S3 through a presigned URL. Rejects oversized or unsupported files before
 * asking the API for a URL.
 */
export function KYCDocumentUpload({ uploadedTypes }: { uploadedTypes: KYCDocumentType[] }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const fileInput = useRef<HTMLInputElement>(null)
  const [docType, setDocType] = useState<KYCDocumentType>('id_front')
  const [preview, setPreview] = useState<{ file: File; url: string } | null>(null)
  const isReplacing = uploadedTypes.includes(docType)
  // Captured at confirm time (not read from `isReplacing` in onSuccess) since
  // `uploadedTypes` may have already changed by the time the mutation settles.
  const wasReplacingRef = useRef(false)

  const { mutate: upload, isPending } = useMutation({
    mutationFn: (file: File) => uploadKYCDocumentAPI(file, docType),
    // The confirm endpoint already returns the authoritative post-upload status;
    // write it straight to the cache so the checklist marks immediately instead
    // of depending on a separate GET that may lag the write.
    onSuccess: (status) => {
      queryClient.setQueryData(['kyc'], status)
      toast.success(wasReplacingRef.current ? t('identity.documentReplaced') : t('identity.uploadSuccess'))
      setPreview(null)
    },
    onError: () => toast.error(t('identity.uploadFailed')),
    onSettled: () => {
      if (fileInput.current) fileInput.current.value = ''
    },
  })

  // Revoke the object URL whenever the preview changes or the component unmounts.
  useEffect(() => {
    if (!preview) return
    return () => URL.revokeObjectURL(preview.url)
  }, [preview])

  function handleFile(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return

    if (file.size > MAX_DOCUMENT_BYTES) {
      toast.error(t('identity.fileTooLarge'))
      e.target.value = ''
      return
    }
    if (!ID_DOCUMENT_ACCEPTED_TYPES.includes(file.type as (typeof ID_DOCUMENT_ACCEPTED_TYPES)[number])) {
      toast.error(t('identity.fileTypeUnsupported'))
      e.target.value = ''
      return
    }
    setPreview({ file, url: URL.createObjectURL(file) })
    e.target.value = ''
  }

  function confirmUpload() {
    if (!preview) return
    wasReplacingRef.current = isReplacing
    upload(preview.file)
  }

  function changeFile() {
    setPreview(null)
  }

  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor="document_type">{t('identity.documentType')}</Label>
        <Select value={docType} onValueChange={(value) => setDocType(value as KYCDocumentType)} disabled={!!preview}>
          <SelectTrigger id="document_type" className="w-full min-h-11">
            <SelectValue>
              {docType ? t(TYPE_LABEL_KEY[docType]) : t('identity.documentType')}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {DOCUMENT_TYPES.map((type) => (
              <SelectItem key={type} value={type}>
                {t(TYPE_LABEL_KEY[type])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {preview ? (
        <div className="space-y-3">
          <p className="text-sm font-medium">{t('identity.documentPreviewTitle')}</p>
          <div className="relative aspect-video w-full max-w-xs sm:max-w-sm overflow-hidden rounded-lg bg-muted">
            {ID_DOCUMENT_PREVIEWABLE_TYPES.includes(preview.file.type as (typeof ID_DOCUMENT_PREVIEWABLE_TYPES)[number]) ? (
              <Image src={preview.url} alt={t(TYPE_LABEL_KEY[docType])} fill unoptimized className="object-cover" />
            ) : (
              <div className="flex h-full flex-col items-center justify-center gap-1.5 p-3 text-center">
                <FileText className="text-muted-foreground size-6" aria-hidden />
                <p className="text-sm font-medium break-all">{preview.file.name}</p>
                <p className="text-muted-foreground text-xs">{t('identity.documentPreviewUnavailable')}</p>
              </div>
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button type="button" variant="outline" className="min-h-11" onClick={changeFile} disabled={isPending}>
              {t('identity.documentPreviewChangeFile')}
            </Button>
            <Button type="button" className="min-h-11" onClick={confirmUpload} disabled={isPending}>
              {isPending ? t('identity.uploading') : t('identity.documentPreviewConfirm')}
            </Button>
          </div>
        </div>
      ) : (
        <>
          <p className="text-muted-foreground text-xs">{t('identity.documentCaptureTips')}</p>
          <input
            ref={fileInput}
            type="file"
            className="sr-only"
            aria-label={t(TYPE_LABEL_KEY[docType])}
            tabIndex={-1}
            accept={ID_DOCUMENT_ACCEPTED_TYPES.join(',')}
            onChange={handleFile}
            disabled={isPending}
          />
          <Button type="button" className="min-h-11" onClick={() => fileInput.current?.click()} disabled={isPending}>
            <Upload className="size-4" />
            {isReplacing ? t('identity.replaceDocument') : t('identity.uploadDocument')}
          </Button>
        </>
      )}
    </div>
  )
}
