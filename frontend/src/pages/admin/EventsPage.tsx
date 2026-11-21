import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { apiPage } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { AdminEvent } from '../../lib/types'
import { ButtonLink, Card, Input, PageHeader, Pagination, ProgressBar, Select, StatusBadge } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'

const statuses = ['draft', 'pending_review', 'published', 'rejected', 'cancelled'].map((s) => ({ value: s, label: s.replace('_', ' ').replace(/^./, (c) => c.toUpperCase()) }))

export default function EventsPage({ reviewQueue = false }: { reviewQueue?: boolean }) {
  const { can } = useAuth()
  const navigate = useNavigate()
  const [q, setQ] = useQueryParam('q')
  const [statusParam, setStatus] = useQueryParam('status')
  const [pageParam, setPage] = useQueryParam('page', '1')
  const query = useDebounced(q, 300)
  const status = reviewQueue ? 'pending_review' : statusParam
  const page = Number(pageParam) || 1

  const events = useQuery({
    queryKey: ['admin', 'events', { query, status, page }],
    queryFn: () => apiPage<AdminEvent>('/admin/events', { q: query, status, page }),
  })

  const columns: Column<AdminEvent>[] = [
    {
      header: 'Event',
      cell: (e) => (
        <div className="min-w-56">
          <p className="font-medium text-zinc-900">{e.title}</p>
          <p className="text-xs text-zinc-500">{e.organiser_name}, {e.venue?.name ?? 'No venue'}</p>
        </div>
      ),
    },
    { header: 'Date', cell: (e) => <span className="whitespace-nowrap">{formatDateTime(e.starts_at, e.timezone)}</span> },
    { header: 'Status', cell: (e) => <div className="flex gap-1"><StatusBadge status={e.status} />{e.status === 'published' && <StatusBadge status={e.phase} />}</div> },
    {
      header: 'Sold',
      cell: (e) => (
        <div className="w-32">
          <p className="text-xs text-zinc-500">{e.stats.tickets_sold} / {e.capacity}</p>
          <ProgressBar value={e.stats.tickets_sold} max={e.capacity} className="mt-1 h-1.5" />
        </div>
      ),
    },
  ]

  return (
    <>
      <PageHeader
        title={reviewQueue ? 'Event approvals' : 'Events'}
        description={reviewQueue ? 'Events submitted by organisers and waiting for review before they go on sale.' : 'Create, publish and manage events.'}
        actions={!reviewQueue && can(P.EventsCreate) && <ButtonLink to="/admin/events/new"><Plus className="size-4" /> New event</ButtonLink>}
      />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Search by title or venue" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search" />
          {!reviewQueue && <Select options={statuses} placeholder="All statuses" value={statusParam} onChange={(e) => setStatus(e.target.value)} className="w-48" aria-label="Status" />}
        </div>
        <DataTable
          columns={columns}
          rows={events.data?.data}
          loading={events.isLoading}
          rowKey={(e) => e.id}
          onRowClick={(e) => navigate(`/admin/events/${e.id}`)}
          empty={reviewQueue ? { title: 'Nothing to review', description: 'Submitted events will appear here.' } : { title: 'No events found' }}
        />
        <Pagination meta={events.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
    </>
  )
}
