import { useState } from 'react'
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useQuery } from '@tanstack/react-query'
import { FileDown, FileSpreadsheet, FileText } from 'lucide-react'
import { api, download, errorMessage } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDate, formatDateTime, formatMoney } from '../../lib/format'
import { useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { ReportColumn, ReportDefinition, ReportTable } from '../../lib/types'
import { EventPicker } from '../../components/EventPicker'
import { Alert, Button, Card, CardHeader, cx, EmptyState, Input, Loading, PageHeader } from '../../components/ui'
import { useToast } from '../../components/toast'

function isoDay(d: Date) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function defaultRange() {
  const now = new Date()
  return { from: isoDay(new Date(now.getFullYear(), now.getMonth() - 2, 1)), to: isoDay(now) }
}

function renderCell(value: unknown, column: ReportColumn, currency: string) {
  if (value === null || value === undefined || value === '') return '—'
  // Labels such as "Total" can sit in any column of the totals row.
  if (typeof value === 'string' && column.kind !== 'text' && Number.isNaN(Date.parse(value))) return value
  switch (column.kind) {
    case 'money':
      return formatMoney(Number(value), currency)
    case 'int':
      return Number(value).toLocaleString()
    case 'percent':
      return `${(Number(value) * 100).toFixed(1)}%` // the API sends a fraction
    case 'datetime':
      return formatDateTime(String(value))
    case 'date':
      return formatDate(String(value), 'UTC') // calendar days arrive as midnight UTC
    default:
      return String(value)
  }
}

const numeric = (c: ReportColumn) => c.kind === 'money' || c.kind === 'int' || c.kind === 'percent'

/** Headline figures from the report's totals row, and a trend line for daily reports. */
function ReportSummary({ table }: { table: ReportTable }) {
  const totals = table.totals
  const figures = totals
    ? table.columns
        .map((c, i) => ({ c, v: totals[i] }))
        .filter(({ c, v }) => (c.kind === 'money' || c.kind === 'int') && typeof v === 'number')
        .slice(0, 4)
    : []
  const dateCol = table.columns.findIndex((c) => c.kind === 'date')
  const valueCol = table.columns.findIndex((c) => c.key === 'net') >= 0 ? table.columns.findIndex((c) => c.key === 'net') : table.columns.findIndex((c) => c.kind === 'money')
  const series = dateCol === 0 && valueCol > 0 ? table.rows.map((r) => ({ date: String(r[dateCol]), value: Number(r[valueCol]) })) : []
  if (!figures.length && series.length < 2) return null
  return (
    <div className="space-y-4">
      {figures.length > 0 && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          {figures.map(({ c, v }) => (
            <Card key={c.key} className="p-5">
              <p className="text-sm text-zinc-500">{c.label}</p>
              <p className="mt-1 font-display text-2xl font-bold text-ink-900">{renderCell(v, c, table.currency)}</p>
            </Card>
          ))}
        </div>
      )}
      {series.length >= 2 && (
        <Card className="p-5">
          <p className="mb-3 text-sm font-semibold text-ink-900">{table.columns[valueCol].label} by day</p>
          <div className="h-56">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={series} margin={{ left: 8, right: 8 }}>
                <CartesianGrid stroke="#f1f0f6" vertical={false} />
                <XAxis dataKey="date" tickFormatter={(d: string) => formatDate(d, 'UTC').replace(/ \d{4}$/, '')} tick={{ fontSize: 12, fill: '#71717a' }} tickLine={false} axisLine={false} minTickGap={20} />
                <YAxis tickFormatter={(v: number) => `R${Math.round(v / 100).toLocaleString()}`} tick={{ fontSize: 12, fill: '#71717a' }} tickLine={false} axisLine={false} width={70} />
                <Tooltip formatter={(v) => [formatMoney(Number(v), table.currency), table.columns[valueCol].label]} labelFormatter={(d) => formatDate(String(d), 'UTC')} cursor={{ fill: '#f3f0ff' }} />
                <Bar dataKey="value" fill="#6a3ff5" radius={[4, 4, 0, 0]} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        </Card>
      )}
    </div>
  )
}

export default function ReportsPage() {
  const { can } = useAuth()
  const toast = useToast()
  const initial = defaultRange()
  const [name, setName] = useQueryParam('report')
  const [from, setFrom] = useQueryParam('from', initial.from)
  const [to, setTo] = useQueryParam('to', initial.to)
  const [eventId, setEventId] = useQueryParam('event')
  const [exporting, setExporting] = useState<string | null>(null)

  const reports = useQuery({ queryKey: ['admin', 'reports'], queryFn: () => api<ReportDefinition[]>('/admin/reports') })
  const selected = reports.data?.find((r) => r.name === name) ?? reports.data?.[0]
  // Reports that cover several events can be narrowed to one; the attendee list needs one.
  const eventFilter = selected?.event_filter ?? 'none'
  const params = { from, to, event_id: eventFilter === 'none' ? '' : eventId }
  const ready = !!selected && (eventFilter !== 'required' || !!eventId)
  const table = useQuery({
    queryKey: ['admin', 'report', selected?.name, params],
    queryFn: () => api<ReportTable>(`/admin/reports/${selected!.name}`, { query: params }),
    enabled: ready,
  })

  const exportAs = async (format: 'csv' | 'xlsx' | 'pdf') => {
    if (!selected) return
    setExporting(format)
    try {
      await download(`/admin/reports/${selected.name}`, { ...params, format }, `${selected.name}.${format}`)
    } catch (e) {
      toast.error(errorMessage(e))
    } finally {
      setExporting(null)
    }
  }

  if (reports.isLoading) return <Loading />
  const t = table.data

  return (
    <>
      <PageHeader title="Reports" description="Sales, revenue, attendance and payment reports. Export any report to Excel, CSV or PDF." />
      <div className="grid gap-6 xl:grid-cols-[260px_1fr]">
        <Card className="h-fit p-2">
          <nav className="space-y-0.5">
            {reports.data?.map((r) => (
              <button
                key={r.name}
                onClick={() => setName(r.name)}
                className={cx('w-full rounded-lg px-3 py-2 text-left text-sm', selected?.name === r.name ? 'bg-brand-50 font-medium text-brand-700' : 'text-zinc-700 hover:bg-zinc-50')}
              >
                {r.title}
              </button>
            ))}
          </nav>
        </Card>

        <div className="min-w-0 space-y-4">
          {selected && (
            <Card>
              <CardHeader
                title={selected.title}
                description={selected.description}
                actions={
                  can(P.ReportsExport) && (
                    <>
                      <Button size="sm" variant="secondary" icon={<FileSpreadsheet className="size-4" />} loading={exporting === 'xlsx'} disabled={!ready} onClick={() => exportAs('xlsx')}>Excel</Button>
                      <Button size="sm" variant="secondary" icon={<FileDown className="size-4" />} loading={exporting === 'csv'} disabled={!ready} onClick={() => exportAs('csv')}>CSV</Button>
                      <Button size="sm" variant="secondary" icon={<FileText className="size-4" />} loading={exporting === 'pdf'} disabled={!ready} onClick={() => exportAs('pdf')}>PDF</Button>
                    </>
                  )
                }
              />
              <div className="flex flex-wrap gap-3 p-4">
                <Input label="From" type="date" value={from} max={to} onChange={(e) => setFrom(e.target.value)} />
                <Input label="To" type="date" value={to} min={from} onChange={(e) => setTo(e.target.value)} />
                {eventFilter !== 'none' && (
                  <EventPicker className="min-w-72" value={eventId} onChange={setEventId} allowAll={eventFilter === 'optional'} />
                )}
              </div>
            </Card>
          )}

          {t && t.rows.length > 0 && <ReportSummary table={t} />}

          <Card className="overflow-hidden">
            {!ready ? (
              <EmptyState title="Choose an event" description="This report covers a single event." />
            ) : table.isLoading ? (
              <Loading label="Running report…" />
            ) : table.error ? (
              <div className="p-5"><Alert tone="error">{errorMessage(table.error)}</Alert></div>
            ) : !t || t.rows.length === 0 ? (
              <EmptyState title="No data for this period" description="Try a wider date range." />
            ) : (
              <div className="overflow-x-auto">
                <table className="min-w-full divide-y divide-zinc-200 text-sm">
                  <thead className="bg-zinc-50">
                    <tr>
                      {t.columns.map((c) => (
                        <th key={c.key} className={cx('whitespace-nowrap px-4 py-2.5 text-xs font-semibold uppercase tracking-wide text-zinc-500', numeric(c) ? 'text-right' : 'text-left')}>
                          {c.label}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-zinc-100">
                    {t.rows.map((row, i) => (
                      <tr key={i}>
                        {t.columns.map((c, j) => (
                          <td key={c.key} className={cx('whitespace-nowrap px-4 py-2.5 text-zinc-700', numeric(c) && 'text-right tabular-nums')}>
                            {renderCell(row[j], c, t.currency)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                  {t.totals && (
                    <tfoot className="bg-zinc-50 font-semibold">
                      <tr>
                        {t.columns.map((c, j) => (
                          <td key={c.key} className={cx('whitespace-nowrap px-4 py-2.5 text-zinc-900', numeric(c) && 'text-right tabular-nums')}>
                            {t.totals![j] == null ? '' : renderCell(t.totals![j], c, t.currency)}
                          </td>
                        ))}
                      </tr>
                    </tfoot>
                  )}
                </table>
                <p className="border-t border-zinc-100 px-4 py-2 text-xs text-zinc-500">{t.rows.length} rows, generated {t.generated_at}</p>
              </div>
            )}
          </Card>
        </div>
      </div>
    </>
  )
}
