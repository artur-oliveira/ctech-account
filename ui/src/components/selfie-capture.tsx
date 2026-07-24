'use client'

import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { uploadKYCDocumentAPI } from '@/lib/mutations'
import { SELFIE_CLIP_CONTENT_TYPE, SELFIE_CLIP_MIME_CANDIDATES } from '@/lib/constants'
import type { KYCDocumentType } from '@/lib/types'
import { Camera, ShieldCheck, XCircle } from 'lucide-react'

const RECORD_MS = 1500
const CAMERA_BLOCKED_THRESHOLD = 2

// Not every browser supports every candidate (Safari/iOS commonly lacks VP8/VP9
// webm) — ask MediaRecorder what it can actually encode instead of assuming webm.
function pickClipMimeType(): string {
  if (typeof MediaRecorder === 'undefined') return SELFIE_CLIP_CONTENT_TYPE
  return SELFIE_CLIP_MIME_CANDIDATES.find((type) => MediaRecorder.isTypeSupported(type)) ?? SELFIE_CLIP_CONTENT_TYPE
}

type CameraErrorReason = 'permission' | 'not-found' | 'in-use' | 'insecure' | 'unsupported' | null

// getUserMedia() DOMException names — https://developer.mozilla.org/docs/Web/API/MediaDevices/getUserMedia
function classifyCameraError(err: unknown): CameraErrorReason {
  const name = err instanceof DOMException ? err.name : ''
  if (name === 'NotFoundError' || name === 'OverconstrainedError') return 'not-found'
  if (name === 'NotReadableError' || name === 'TrackStartError') return 'in-use'
  if (name === 'SecurityError') return 'insecure'
  return 'permission'
}

const POSES: { type: KYCDocumentType; instructionKey: string; shortLabelKey: string }[] = [
  { type: 'selfie_up', instructionKey: 'identity.selfieUp', shortLabelKey: 'identity.poseUp' },
  { type: 'selfie_down', instructionKey: 'identity.selfieDown', shortLabelKey: 'identity.poseDown' },
  { type: 'selfie_left', instructionKey: 'identity.selfieLeft', shortLabelKey: 'identity.poseLeft' },
  { type: 'selfie_right', instructionKey: 'identity.selfieRight', shortLabelKey: 'identity.poseRight' },
]

/**
 * Records four short head-turn clips (up/down/left/right) instead of one
 * still photo. A printed photo or looped video can't turn on command — the
 * reviewer watches the clips and judges real-vs-photo, no server-side ML.
 */
export function SelfieCapture({ uploadedTypes }: { uploadedTypes: KYCDocumentType[] }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const videoRef = useRef<HTMLVideoElement>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const progressRef = useRef<HTMLDivElement>(null)
  const recordTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const previewHeadingRef = useRef<HTMLParagraphElement>(null)
  // Guards recorder.onstop, which the browser still fires after the effect
  // cleanup stops the stream tracks (e.g. unmounting/switching pose mid-recording)
  // — without it, a blob URL gets created for a preview that can never render
  // or be revoked, leaking for the life of the page.
  const recordingActiveRef = useRef(true)
  const [cameraError, setCameraError] = useState<CameraErrorReason>(null)
  const [recording, setRecording] = useState(false)
  const [consented, setConsented] = useState(false)
  const [retakeType, setRetakeType] = useState<KYCDocumentType | null>(null)
  const [retryCount, setRetryCount] = useState(0)
  const [deniedAttempts, setDeniedAttempts] = useState(0)
  const [preview, setPreview] = useState<{ url: string; blob: Blob } | null>(null)

  const pose = retakeType
    ? POSES.find((p) => p.type === retakeType)
    : POSES.find((p) => !uploadedTypes.includes(p.type))

  const { mutate: upload, isPending } = useMutation({
    mutationFn: (args: { type: KYCDocumentType; blob: Blob }) => {
      const extension = args.blob.type.includes('mp4') ? 'mp4' : 'webm'
      return uploadKYCDocumentAPI(new File([args.blob], `${args.type}.${extension}`, { type: args.blob.type || SELFIE_CLIP_CONTENT_TYPE }), args.type)
    },
    // The confirm endpoint already returns the authoritative post-upload status;
    // write it straight to the cache so the checklist marks immediately instead
    // of depending on a separate GET that may lag the write.
    onSuccess: (status) => {
      queryClient.setQueryData(['kyc'], status)
      toast.success(t('identity.uploadSuccess'))
      setRetakeType(null)
      setPreview(null)
    },
    onError: () => toast.error(t('identity.uploadFailed')),
  })

  // Revoke the object URL whenever the preview clip changes or the component unmounts.
  useEffect(() => {
    if (!preview) return
    return () => URL.revokeObjectURL(preview.url)
  }, [preview])

  // Nothing else signals that recording finished and a new review panel
  // replaced the camera view — move focus there so it's announced.
  useEffect(() => {
    if (preview) previewHeadingRef.current?.focus()
  }, [preview])

  useEffect(() => {
    if (!pose || !consented) return
    recordingActiveRef.current = true
    if (!navigator.mediaDevices?.getUserMedia) {
      Promise.resolve().then(() => setCameraError('unsupported'))
      return
    }
    let active = true
    navigator.mediaDevices
      .getUserMedia({ video: { facingMode: 'user' }, audio: false })
      .then((stream) => {
        if (!active) {
          stream.getTracks().forEach((track) => track.stop())
          return
        }
        streamRef.current = stream
        if (videoRef.current) videoRef.current.srcObject = stream
        setCameraError(null)
        setDeniedAttempts(0)
      })
      .catch((err) => {
        const reason = classifyCameraError(err)
        setCameraError(reason)
        if (reason === 'permission') setDeniedAttempts((n) => n + 1)
      })

    return () => {
      active = false
      recordingActiveRef.current = false
      streamRef.current?.getTracks().forEach((track) => track.stop())
      streamRef.current = null
      if (recordTimeoutRef.current) {
        clearTimeout(recordTimeoutRef.current)
        recordTimeoutRef.current = null
      }
    }
    // Re-open the camera for each new pose, when consent first flips true, and
    // on each retry click — pose is already resolved before the consent click,
    // so pose.type alone never changes at that moment and the effect must
    // watch consented too or the camera never opens for the first pose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pose?.type, consented, retryCount])

  function record() {
    const stream = streamRef.current
    if (!stream || !pose) return

    chunksRef.current = []
    const mimeType = pickClipMimeType()
    const recorder = new MediaRecorder(stream, { mimeType })
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunksRef.current.push(e.data)
    }
    recorder.onstop = () => {
      setRecording(false)
      progressRef.current?.getAnimations().forEach((a) => a.cancel())
      // The browser still fires onstop after cleanup stops the tracks (pose
      // switch, retry, or unmount mid-recording) — bail out before creating a
      // blob URL that would never be revoked.
      if (!recordingActiveRef.current) return
      const blob = new Blob(chunksRef.current, { type: recorder.mimeType || mimeType })
      setPreview({ url: URL.createObjectURL(blob), blob })
    }
    recorder.start()
    setRecording(true)

    // Lets the user judge whether they held the pose long enough — otherwise
    // "Recording…" gives no sense of the fixed 1.5s window closing.
    const reduceMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    if (progressRef.current && !reduceMotion) {
      progressRef.current.animate([{ transform: 'scaleX(0)' }, { transform: 'scaleX(1)' }], {
        duration: RECORD_MS,
        easing: 'linear',
        fill: 'forwards',
      })
    }

    recordTimeoutRef.current = setTimeout(() => recorder.stop(), RECORD_MS)
  }

  function confirmClip() {
    if (!preview || !pose) return
    upload({ type: pose.type, blob: preview.blob })
  }

  function retakeClip() {
    setPreview(null)
  }

  function cancelSession() {
    if (recordTimeoutRef.current) {
      clearTimeout(recordTimeoutRef.current)
      recordTimeoutRef.current = null
    }
    setConsented(false)
    setPreview(null)
    setCameraError(null)
  }

  if (!pose) {
    return (
      <div className="space-y-2">
        <p className="text-sm font-medium">{t('identity.selfieAllDone')}</p>
        <ul className="flex flex-wrap gap-3">
          {POSES.map((p) => (
            <li key={p.type}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => setRetakeType(p.type)}
                className="min-h-11 text-muted-foreground hover:text-foreground"
              >
                <span className="bg-primary size-2 shrink-0 rounded-full" aria-hidden />
                {t('identity.retake', { pose: t(p.shortLabelKey) })}
              </Button>
            </li>
          ))}
        </ul>
      </div>
    )
  }

  if (!consented) {
    return (
      <Alert>
        <ShieldCheck className="size-4" />
        <AlertDescription className="space-y-3">
          <p className="text-foreground font-medium">{t('identity.selfieConsentTitle')}</p>
          <p>{t('identity.selfieConsentBody')}</p>
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
        <p ref={previewHeadingRef} tabIndex={-1} className="text-sm font-medium outline-none">
          {t('identity.selfiePreviewTitle')}
        </p>
        <p aria-live="polite" className="sr-only">
          {t('identity.selfieClipReady')}
        </p>
        {/* Mirrored to match what was just seen live (selfieHint's "look
            left/right" was followed against a mirrored self-view) — the
            uploaded blob itself is untouched, this only affects the user's
            own review of it. */}
        <video
          src={preview.url}
          controls
          playsInline
          className="aspect-3/4 w-full max-w-xs sm:max-w-sm -scale-x-100 rounded-lg bg-muted object-cover"
        />
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" className="min-h-11" onClick={retakeClip} disabled={isPending}>
            {t('identity.selfiePreviewRetake')}
          </Button>
          <Button type="button" className="min-h-11" onClick={confirmClip} disabled={isPending}>
            {isPending ? t('identity.uploading') : t('identity.selfiePreviewKeep')}
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
            : deniedAttempts >= CAMERA_BLOCKED_THRESHOLD
              ? t('identity.cameraBlocked')
              : t('identity.cameraDenied')

  const posesDone = POSES.filter((p) => uploadedTypes.includes(p.type)).length

  return (
    <div className="space-y-3">
      <p className="text-sm font-medium">{t(pose.instructionKey)}</p>
      <p className="text-muted-foreground text-xs">{t('identity.selfieHint')}</p>

      {cameraError ? (
        <Alert variant="destructive">
          <XCircle className="size-4" />
          <AlertDescription className="space-y-2">
            <p>{cameraErrorMessage}</p>
            <div className="flex items-center gap-2">
              <Button type="button" variant="outline" size="sm" className="min-h-11" onClick={() => setRetryCount((n) => n + 1)}>
                {t('identity.cameraRetry')}
              </Button>
              <Button type="button" variant="ghost" size="sm" className="min-h-11" onClick={cancelSession}>
                {t('identity.selfieCancel')}
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      ) : (
        <video
          ref={videoRef}
          autoPlay
          muted
          playsInline
          className="aspect-3/4 w-full max-w-xs sm:max-w-sm -scale-x-100 rounded-lg bg-muted object-cover"
        ></video>
      )}

      {!cameraError && (
        <div className="bg-muted h-1 w-full max-w-xs overflow-hidden rounded-full" aria-hidden>
          <div ref={progressRef} className="bg-primary h-full w-full origin-left scale-x-0" />
        </div>
      )}
      <p aria-live="polite" className="sr-only">
        {recording ? t('identity.recording') : ''}
      </p>

      <div className="flex items-center gap-2">
        <Button type="button" className="min-h-11" onClick={record} disabled={!!cameraError || recording || isPending}>
          <Camera className="size-4" />
          {recording ? t('identity.recording') : isPending ? t('identity.uploading') : t('identity.recordPose')}
        </Button>
        <Button type="button" variant="outline" className="min-h-11" onClick={cancelSession} disabled={recording || isPending}>
          {t('identity.selfieCancel')}
        </Button>
        <div className="flex gap-1">
          <span className="sr-only">{t('identity.progressLabel', { done: posesDone, total: POSES.length })}</span>
          {POSES.map((p) => (
            <span
              key={p.type}
              aria-hidden
              className={`size-2 rounded-full ${uploadedTypes.includes(p.type) ? 'bg-primary' : 'bg-muted'}`}
            />
          ))}
        </div>
      </div>
    </div>
  )
}
