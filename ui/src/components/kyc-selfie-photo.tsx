'use client'

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { uploadKYCDocumentAPI } from '@/lib/mutations'
import { Camera, ShieldCheck, XCircle } from 'lucide-react'

type CameraErrorReason = 'permission' | 'not-found' | 'in-use' | 'insecure' | 'unsupported' | null

function classifyCameraError(err: unknown): CameraErrorReason {
  const name = err instanceof DOMException ? err.name : ''
  if (name === 'NotFoundError' || name === 'OverconstrainedError') return 'not-found'
  if (name === 'NotReadableError' || name === 'TrackStartError') return 'in-use'
  if (name === 'SecurityError') return 'insecure'
  return 'permission'
}

/**
 * Captures a single static photo of the user holding their identity document
 * (selfie_with_document) — replaces the old four-clip head-turn video flow.
 * The human reviewer still judges real-vs-photo; there is no server-side ML.
 */
export function KYCSelfiePhoto({ uploaded }: { uploaded: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const videoRef = useRef<HTMLVideoElement>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const [cameraError, setCameraError] = useState<CameraErrorReason>(null)
  const [consented, setConsented] = useState(false)
  const [retake, setRetake] = useState(false)
  const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null)

  const active = retake || !uploaded

  const { mutate: upload, isPending } = useMutation({
    mutationFn: (blob: Blob) => uploadKYCDocumentAPI(new File([blob], 'selfie_with_document.jpg', { type: 'image/jpeg' }), 'selfie_with_document'),
    onSuccess: (status) => {
      queryClient.setQueryData(['kyc'], status)
      toast.success(t('identity.uploadSuccess'))
      setPreview(null)
      setRetake(false)
    },
    onError: () => toast.error(t('identity.uploadFailed')),
  })

  useEffect(() => {
    if (!preview) return
    return () => URL.revokeObjectURL(preview.url)
  }, [preview])

  useEffect(() => {
    if (!active || !consented || preview) return
    if (!navigator.mediaDevices?.getUserMedia) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setCameraError('unsupported')
      return
    }
    let alive = true
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'user' }, audio: false })
      .then((stream) => {
        if (!alive) {
          stream.getTracks().forEach((track) => track.stop())
          return
        }
        streamRef.current = stream
        if (videoRef.current) videoRef.current.srcObject = stream
        setCameraError(null)
      })
      .catch((err) => setCameraError(classifyCameraError(err)))

    return () => {
      alive = false
      streamRef.current?.getTracks().forEach((track) => track.stop())
      streamRef.current = null
    }
  }, [active, consented, preview])

  function capture() {
    const video = videoRef.current
    if (!video) return
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth
    canvas.height = video.videoHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.drawImage(video, 0, 0)
    canvas.toBlob((blob) => {
      if (blob) setPreview({ url: URL.createObjectURL(blob), blob })
    }, 'image/jpeg', 0.92)
  }

  function confirm() {
    if (!preview) return
    upload(preview.blob)
  }

  if (!active) {
    return (
      <div className="flex items-center gap-2">
        <p className="text-sm font-medium">{t('identity.selfieWithDocumentDone')}</p>
        <Button type="button" variant="ghost" size="sm" className="min-h-11" onClick={() => setRetake(true)}>
          {t('identity.retakePhoto')}
        </Button>
      </div>
    )
  }

  if (!consented) {
    return (
      <Alert>
        <ShieldCheck className="size-4" />
        <AlertDescription className="space-y-3">
          <p className="text-foreground font-medium">{t('identity.selfieConsentTitle')}</p>
          <p>{t('identity.selfieWithDocumentConsentBody')}</p>
          <Button type="button" className="min-h-11" onClick={() => setConsented(true)}>
            <Camera className="size-4" />
            {t('identity.selfieConsentCta')}
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  if (preview) {
    return (
      <div className="space-y-3">
        <p className="text-sm font-medium">{t('identity.documentPreviewTitle')}</p>
        {/* eslint-disable-next-line @next/next/no-img-element -- object URL, next/image can't optimize it */}
        <img src={preview.url} alt={t('identity.selfieWithDocument')} className="aspect-3/4 w-full max-w-xs sm:max-w-sm rounded-lg bg-muted object-cover" />
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" className="min-h-11" onClick={() => setPreview(null)} disabled={isPending}>
            {t('identity.retakePhoto')}
          </Button>
          <Button type="button" className="min-h-11" onClick={confirm} disabled={isPending}>
            {isPending ? t('identity.uploading') : t('identity.documentPreviewConfirm')}
          </Button>
        </div>
      </div>
    )
  }

  const cameraErrorMessage =
    cameraError === 'not-found'
      ? t('identity.cameraNotFound')
      : cameraError === 'in-use'
        ? t('identity.cameraInUse')
        : cameraError === 'insecure'
          ? t('identity.cameraInsecure')
          : cameraError === 'unsupported'
            ? t('identity.cameraUnsupported')
            : t('identity.cameraDenied')

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">{t('identity.selfieWithDocumentInstruction')}</p>

      {cameraError ? (
        <Alert variant="destructive">
          <XCircle className="size-4" />
          <AlertDescription>{cameraErrorMessage}</AlertDescription>
        </Alert>
      ) : (
        <video ref={videoRef} autoPlay muted playsInline className="aspect-3/4 w-full max-w-xs sm:max-w-sm -scale-x-100 rounded-lg bg-muted object-cover" />
      )}

      <Button type="button" className="min-h-11" onClick={capture} disabled={!!cameraError}>
        <Camera className="size-4" />
        {t('identity.capturePhoto')}
      </Button>
    </div>
  )
}
