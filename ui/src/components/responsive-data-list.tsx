'use client'

import * as React from 'react'
import { cn } from '@/lib/utils'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

export type Column<T> = {
  /** Stable key (also used as the mobile card label). */
  key: string
  /** Column header in the table view. */
  header: React.ReactNode
  /** Cell renderer. */
  cell: (row: T) => React.ReactNode
  /** Hide this column's header/label in the mobile card (e.g. the title). */
  title?: boolean
  /** Right-align numeric/date columns. */
  align?: 'left' | 'right'
  /** Fixed table width (e.g. `w-[10rem]`). */
  className?: string
}

type ResponsiveDataListProps<T> = {
  rows: T[]
  columns: Column<T>[]
  /** Right-aligned action controls, rendered in the card footer and last table column. */
  actions?: (row: T) => React.ReactNode
  /** Rendered when `rows` is empty. */
  empty?: React.ReactNode
  /** Row key for React lists. */
  rowKey: (row: T) => string
}

/**
 * One definition, two presentations: a stacked card list on mobile and a
 * scannable `Table` at `md+`. Follows DESIGN.md §5 (tables for sessions, API
 * keys, OAuth clients, activity) while staying usable on small screens.
 */
export function ResponsiveDataList<T>({
  rows,
  columns,
  actions,
  empty,
  rowKey,
}: ResponsiveDataListProps<T>) {
  const titleCol = columns.find((c) => c.title) ?? columns[0]
  const detailCols = columns.filter((c) => c !== titleCol)

  if (rows.length === 0 && empty) {
    return <>{empty}</>
  }

  return (
    <>
      {/* Mobile: stacked cards */}
      <ul className="space-y-3 md:hidden">
        {rows.map((row) => (
          <li key={rowKey(row)} className="rounded-lg border bg-card p-3">
            <div className="flex items-center gap-2">
              <div className="min-w-0 flex-1">{titleCol.cell(row)}</div>
              {actions?.(row)}
            </div>
            {detailCols.length > 0 && (
              <dl className="mt-2 space-y-1">
                {detailCols.map((col) => (
                  <div key={col.key} className="flex items-start gap-2 text-sm">
                    <dt className="shrink-0 text-muted-foreground">{col.header}</dt>
                    <dd className="min-w-0 flex-1 text-right">{col.cell(row)}</dd>
                  </div>
                ))}
              </dl>
            )}
          </li>
        ))}
      </ul>

      {/* Desktop: table */}
      <div className="hidden md:block">
        <Table>
          <TableHeader>
            <TableRow>
              {columns.map((col) => (
                <TableHead
                  key={col.key}
                  className={cn(col.align === 'right' && 'text-right', col.className)}
                >
                  {col.header}
                </TableHead>
              ))}
              {actions && <TableHead className="w-32 text-right">{''}</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <TableRow key={rowKey(row)}>
                {columns.map((col) => (
                  <TableCell
                    key={col.key}
                    className={cn(col.align === 'right' && 'text-right', col.className)}
                  >
                    {col.cell(row)}
                  </TableCell>
                ))}
                {actions && (
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">{actions(row)}</div>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </>
  )
}
