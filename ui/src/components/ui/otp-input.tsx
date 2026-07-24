'use client'

import { useRef } from 'react'
import { cn } from '@/lib/utils'

const OTP_LENGTH = 6

type OTPInputProps = {
  /** Current code value (digits only, up to `length`). */
  value: string
  onChange: (value: string) => void
  length?: number
  disabled?: boolean
  /** Forwarded to the first box so `<Label htmlFor>` still works. */
  id?: string
  className?: string
}

/**
 * Segmented one-time-code input: one box per digit with auto-advance,
 * backspace navigation, and full-code paste. Digits only.
 */
export function OTPInput({ value, onChange, length = OTP_LENGTH, disabled, id, className }: OTPInputProps) {
  const refs = useRef<(HTMLInputElement | null)[]>([])

  function focusBox(index: number) {
    refs.current[Math.max(0, Math.min(index, length - 1))]?.focus()
  }

  function setDigits(digits: string, caretAfter: number) {
    onChange(digits.slice(0, length))
    focusBox(caretAfter)
  }

  function handleChange(index: number, raw: string) {
    const digits = raw.replace(/\D/g, '')
    if (!digits) return
    // Typing (or OS autofill) may deliver several digits at once — spread them
    // forward starting at this box.
    const next = (value.slice(0, index) + digits).slice(0, length)
    setDigits(next, next.length)
  }

  function handleKeyDown(index: number, e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Backspace') {
      e.preventDefault()
      if (value[index]) {
        setDigits(value.slice(0, index) + value.slice(index + 1), index)
      } else if (index > 0) {
        setDigits(value.slice(0, index - 1) + value.slice(index), index - 1)
      }
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault()
      focusBox(index - 1)
    } else if (e.key === 'ArrowRight') {
      e.preventDefault()
      focusBox(index + 1)
    }
  }

  function handlePaste(e: React.ClipboardEvent<HTMLInputElement>) {
    e.preventDefault()
    const digits = e.clipboardData.getData('text').replace(/\D/g, '')
    if (digits) setDigits(digits, digits.length)
  }

  return (
    <div className={cn('flex gap-2', className)}>
      {Array.from({ length }, (_, i) => (
        <input
          key={i}
          id={i === 0 ? id : undefined}
          ref={(el) => {
            refs.current[i] = el
          }}
          type="text"
          inputMode="numeric"
          autoComplete={i === 0 ? 'one-time-code' : 'off'}
          disabled={disabled}
          value={value[i] ?? ''}
          onChange={(e) => handleChange(i, e.target.value)}
          onKeyDown={(e) => handleKeyDown(i, e)}
          onPaste={handlePaste}
          onFocus={(e) => e.target.select()}
          aria-label={`Digit ${i + 1}`}
          className={cn(
            'size-10 rounded-md border border-input bg-transparent text-center text-lg font-mono',
            'outline-none focus-visible:ring-3 focus-visible:ring-ring/70 focus-visible:border-ring',
            'disabled:cursor-not-allowed disabled:opacity-50',
          )}
        />
      ))}
    </div>
  )
}
