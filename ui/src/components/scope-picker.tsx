'use client'

import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { fetchScopeCatalog } from '@/lib/queries'
import { QueryError } from '@/components/query-error'
import { Button } from '@/components/ui/button'
import type { ScopeEntry, ScopeService } from '@/lib/types'

export const IDENTITY_SERVICE = 'identity'

type ScopePickerProps = {
  /** Currently selected scopes. */
  value: string[]
  onChange: (scopes: string[]) => void
  /** Show the identity (OIDC) group — OAuth clients yes, API keys no. */
  includeIdentity?: boolean
}

function entryDescription(entry: ScopeEntry, language: string): string {
  return language.startsWith('pt') ? entry.description_pt : entry.description
}

function ServiceGroup({
  service,
  selected,
  onChange,
  language,
}: {
  service: ScopeService
  selected: Set<string>
  onChange: (scopes: string[]) => void
  language: string
}) {
  const [open, setOpen] = useState(true)
  const codes = service.scopes.map((e) => e.scope)
  const selectedCount = codes.filter((c) => selected.has(c)).length
  const allSelected = selectedCount === codes.length
  const someSelected = selectedCount > 0 && !allSelected

  function toggleAll() {
    const next = new Set(selected)
    if (allSelected) codes.forEach((c) => next.delete(c))
    else codes.forEach((c) => next.add(c))
    onChange([...next])
  }

  function toggleOne(code: string) {
    const next = new Set(selected)
    if (next.has(code)) next.delete(code)
    else next.add(code)
    onChange([...next])
  }

  const Chevron = open ? ChevronDown : ChevronRight

  return (
    <fieldset className="rounded-md border">
      <legend className="sr-only">{service.name}</legend>
      <div className="flex items-center gap-2 px-3 py-2 bg-muted/50">
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={() => setOpen(!open)}
          className="text-foreground/60 hover:text-foreground"
          aria-label={service.name}
          aria-expanded={open}
        >
          <Chevron className="size-4" />
        </Button>
        <label className="flex items-center gap-2 cursor-pointer text-sm font-medium flex-1">
          <input
            type="checkbox"
            checked={allSelected}
            ref={(el) => {
              if (el) el.indeterminate = someSelected
            }}
            onChange={toggleAll}
          />
          {service.name}
        </label>
        {selectedCount > 0 && (
          <span className="text-xs text-foreground/60">{selectedCount}/{codes.length}</span>
        )}
      </div>
      {open && (
        <ul className="px-3 py-2 space-y-1.5">
          {service.scopes.map((entry) => (
            <li key={entry.scope}>
              <label className="flex items-start gap-2 cursor-pointer text-sm">
                <input
                  type="checkbox"
                  className="mt-0.5"
                  checked={selected.has(entry.scope)}
                  onChange={() => toggleOne(entry.scope)}
                />
                <span className="flex-1 min-w-0">
                  {entryDescription(entry, language)}{' '}
                  <code className="text-xs text-muted-foreground font-mono">({entry.scope})</code>
                </span>
              </label>
            </li>
          ))}
        </ul>
      )}
    </fieldset>
  )
}

/**
 * Tree-view scope selector fed by GET /v1.0/scopes — fixed options grouped by
 * service, with select-all per service, instead of free-form scope input.
 */
export function ScopePicker({ value, onChange, includeIdentity = false }: ScopePickerProps) {
  const { t, i18n } = useTranslation()
  const { data: services = [], isLoading, isError, error, refetch } = useQuery({
    queryKey: ['scope-catalog'],
    queryFn: fetchScopeCatalog,
    staleTime: 5 * 60_000,
  })

  const visible = services.filter((s) => includeIdentity || s.service !== IDENTITY_SERVICE)
  const selected = new Set(value)

  if (isLoading) {
    return <div className="h-24 animate-pulse bg-muted rounded-md" />
  }
  if (isError) {
    return <QueryError error={error} onRetry={() => refetch()} />
  }
  if (visible.length === 0) {
    return <p className="text-sm text-muted-foreground">{t('scopePicker.empty')}</p>
  }

  return (
    <div className="space-y-2 max-h-64 overflow-y-auto pr-1">
      {visible.map((svc) => (
        <ServiceGroup
          key={svc.service}
          service={svc}
          selected={selected}
          onChange={onChange}
          language={i18n.language}
        />
      ))}
    </div>
  )
}
