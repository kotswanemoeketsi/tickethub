import { useNavigate } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { apiPage } from '../../lib/api'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import type { OrderSummary } from '../../lib/types'
import { Card, Input, PageHeader, Pagination, Select } from '../../components/ui'
import { OrderTable } from './event/PeopleTabs'

export default function OrdersPage() {
  const navigate = useNavigate()
  const [q, setQ] = useQueryParam('q')
  const [status, setStatus] = useQueryParam('status')
  const [source, setSource] = useQueryParam('source')
  const [page, setPage] = useQueryParam('page', '1')
  const query = useDebounced(q, 300)
  const orders = useQuery({
    queryKey: ['admin', 'orders', { query, status, source, page }],
    queryFn: () => apiPage<OrderSummary>('/admin/orders', { q: query, status, source, page }),
  })
  return (
    <>
      <PageHeader title="Orders" description="Every order across the events you can see." />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Reference, name or email" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search orders" />
          <Select
            aria-label="Status"
            className="w-48"
            placeholder="All statuses"
            value={status}
            onChange={(e) => setStatus(e.target.value)}
            options={['pending', 'paid', 'partially_refunded', 'refunded', 'expired', 'cancelled'].map((s) => ({ value: s, label: s.replace('_', ' ').replace(/^./, (c) => c.toUpperCase()) }))}
          />
          <Select
            aria-label="Source"
            className="w-48"
            placeholder="All sources"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            options={[{ value: 'online', label: 'Purchases' }, { value: 'complimentary', label: 'Complimentary' }]}
          />
        </div>
        <OrderTable rows={orders.data?.data} loading={orders.isLoading} onOpen={(o) => navigate(`/admin/orders/${o.id}`)} />
        <Pagination meta={orders.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
    </>
  )
}
