import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { apiPage } from '../../lib/api'
import { formatDate, formatDateTime, formatMoney } from '../../lib/format'
import type { OrderSummary } from '../../lib/types'
import { ButtonLink, Card, Pagination, StatusBadge } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'

export function MyOrdersPage() {
  const [page, setPage] = useState(1)
  const navigate = useNavigate()
  const orders = useQuery({ queryKey: ['my-orders', page], queryFn: () => apiPage<OrderSummary>('/orders', { page }) })

  const columns: Column<OrderSummary>[] = [
    { header: 'Order', cell: (o) => <span className="font-mono font-medium text-zinc-900">{o.reference}</span> },
    { header: 'Event', cell: (o) => <div><p className="font-medium text-zinc-900">{o.event_title}</p><p className="text-xs text-zinc-500">{formatDate(o.event_starts_at, o.event_timezone)}</p></div> },
    { header: 'Tickets', cell: (o) => o.ticket_count, align: 'right' },
    { header: 'Total', cell: (o) => formatMoney(o.total_cents, o.currency), align: 'right' },
    { header: 'Status', cell: (o) => <StatusBadge status={o.status} /> },
    { header: 'Placed', cell: (o) => formatDateTime(o.created_at) },
  ]

  return (
    <div className="mx-auto max-w-5xl px-4 py-8">
      <h1 className="mb-6 text-2xl font-semibold tracking-tight">My orders</h1>
      <Card>
        <DataTable
          columns={columns}
          rows={orders.data?.data}
          loading={orders.isLoading}
          rowKey={(o) => o.id}
          onRowClick={(o) => navigate(`/account/orders/${o.id}`)}
          empty={{ title: 'No orders yet', description: 'When you buy tickets, your orders appear here.', action: <ButtonLink to="/">Browse events</ButtonLink> }}
        />
        <Pagination meta={orders.data?.meta} onPage={setPage} />
      </Card>
    </div>
  )
}
