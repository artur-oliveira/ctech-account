import i18n from './i18n'

function locale(): string {
  return i18n.language || 'pt-BR'
}

export function formatDistanceToNow(dateStr: string): string {
  if (!dateStr) return '—'
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSecs = Math.floor(diffMs / 1000)
  const diffMins = Math.floor(diffSecs / 60)
  const diffHours = Math.floor(diffMins / 60)
  const diffDays = Math.floor(diffHours / 24)

  const rtf = new Intl.RelativeTimeFormat(locale(), { numeric: 'auto' })

  if (diffSecs < 60) return rtf.format(0, 'second')
  if (diffMins < 60) return rtf.format(-diffMins, 'minute')
  if (diffHours < 24) return rtf.format(-diffHours, 'hour')
  if (diffDays < 30) return rtf.format(-diffDays, 'day')
  return date.toLocaleDateString(locale(), { year: 'numeric', month: 'short', day: 'numeric' })
}

export function formatDate(dateStr: string | null): string {
  if (!dateStr) return '—'
  return new Date(dateStr).toLocaleDateString(locale(), {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}
