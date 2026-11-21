import type { ReactNode } from 'react'
import { cx, EmptyState, Loading } from './ui'

export interface Column<T> {
  header: ReactNode
  cell: (row: T) => ReactNode
  className?: string
  align?: 'left' | 'right'
}

interface DataTableProps<T> {
  columns: Column<T>[]
  rows: T[] | undefined
  rowKey: (row: T) => string | number
  loading?: boolean
  empty?: { title: string; description?: string; action?: ReactNode }
  onRowClick?: (row: T) => void
}

/** A responsive table: horizontally scrollable on small screens. */
export function DataTable<T>({ columns, rows, rowKey, loading, empty, onRowClick }: DataTableProps<T>) {
  if (loading && !rows) return <Loading />
  if (!rows || rows.length === 0) {
    return <EmptyState title={empty?.title ?? 'Nothing here yet'} description={empty?.description} action={empty?.action} />
  }
  return (
    <div className="overflow-x-auto">
      <table className="min-w-full divide-y divide-zinc-200 text-sm">
        <thead className="bg-zinc-50">
          <tr>
            {columns.map((c, i) => (
              <th
                key={i}
                scope="col"
                className={cx('whitespace-nowrap px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-zinc-500', c.align === 'right' ? 'text-right' : 'text-left', c.className)}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-zinc-100 bg-white">
          {rows.map((row) => (
            <tr key={rowKey(row)} onClick={onRowClick ? () => onRowClick(row) : undefined} className={cx(onRowClick && 'cursor-pointer hover:bg-zinc-50')}>
              {columns.map((c, i) => (
                <td key={i} className={cx('px-4 py-3 align-middle text-zinc-700', c.align === 'right' && 'text-right tabular-nums', c.className)}>
                  {c.cell(row)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
