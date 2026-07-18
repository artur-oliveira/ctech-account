'use client'

import { useState, type ReactElement } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { AlertTriangle, Loader2Icon } from 'lucide-react'

type ConfirmDialogProps = {
  /** Element rendered as the trigger (typically a <Button>). */
  trigger: ReactElement
  /** Bold, short title — the action being confirmed. */
  title: string
  /** Consequence copy: what happens if the user confirms. */
  description: string
  confirmLabel?: string
  cancelLabel?: string
  /** Destructive (red confirm) for irreversible/risky actions; default otherwise. */
  variant?: 'default' | 'destructive'
  onConfirm: () => void | Promise<void>
}

/**
 * Branded replacement for the native confirm(). Keeps the consequence in plain
 * view, never blocks the thread, and inherits the app's focus/contrast tokens.
 */
export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel,
  cancelLabel,
  variant = 'destructive',
  onConfirm,
}: ConfirmDialogProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)

  async function handleConfirm() {
    setPending(true)
    try {
      await onConfirm()
      setOpen(false)
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog
      open={open}
      // Suppress close (Esc / overlay / X) while the mutation is in flight —
      // matches the DESIGN.md ConfirmDialog contract: overlay/Esc close is
      // suppressed mid-flight so the action can't look cancelled while it commits.
      onOpenChange={(next) => {
        if (!next && pending) return
        setOpen(next)
      }}
    >
      <DialogTrigger render={trigger} />
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {variant === 'destructive' && <AlertTriangle className="size-4 text-destructive" />}
            {title}
          </DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {/* The trigger (often re-labeled "Submitting…" by the caller) sits behind
            this modal once open, so it can never carry the pending feedback —
            the dialog itself must show it. */}
        <span aria-live="polite" className="sr-only">
          {pending ? t('dialog.processing') : ''}
        </span>
        <DialogFooter showCloseButton={false}>
          <DialogClose
            render={
              <Button variant="outline" className="min-h-11" disabled={pending}>
                {cancelLabel ?? t('dialog.cancel')}
              </Button>
            }
          />
          <Button variant={variant} className="min-h-11" disabled={pending} onClick={handleConfirm}>
            {pending && <Loader2Icon className="size-4 animate-spin" aria-hidden />}
            {confirmLabel ?? t('dialog.confirm')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
