import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { apiPage } from '../../lib/api'
import { formatDateTime, humanize } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import type { AuditLog } from '../../lib/types'
import { Card, EmptyState, Input, Loading, PageHeader, Pagination } from '../../components/ui'

function Row({ log }: { log: AuditLog }) {
  const [open, setOpen] = useState(false)
  const hasChanges = log.changes !== undefined && log.changes !== null
  return (
    <li className="px-5 py-3 text-sm">
      <button className="flex w-full items-start gap-3 text-left" onClick={() => hasChanges && setOpen(!open)} disabled={!hasChanges}>
        <span className="mt-0.5 w-4 text-zinc-400">{hasChanges && (open ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />)}</span>
        <div className="min-w-0 flex-1">
          <p className="text-zinc-900">{log.summary}</p>
          <p className="mt-0.5 text-xs text-zinc-500">
            <span className="font-mono">{log.action}</span>, {log.actor_email || 'System'}, {humanize(log.entity_type)}
            {log.entity_id && ` #${log.entity_id}`}
            {log.ip_address && `, ${log.ip_address}`}
          </p>
        </div>
        <span className="whitespace-nowrap text-xs text-zinc-500">{formatDateTime(log.created_at)}</span>
      </button>
      {open && <pre className="ml-7 mt-2 overflow-x-auto rounded-lg bg-zinc-50 p-3 text-xs text-zinc-700">{JSON.stringify(log.changes, null, 2)}</pre>}
    </li>
  )
}

export default function AuditPage() {
  const [action, setAction] = useQueryParam('action')
  const [actor, setActor] = useQueryParam('actor')
  const [entity, setEntity] = useQueryParam('entity_type')
  const [page, setPage] = useQueryParam('page', '1')
  const filters = { action: useDebounced(action, 300), actor: useDebounced(actor, 300), entity_type: useDebounced(entity, 300) }
  const logs = useQuery({ queryKey: ['admin', 'audit', filters, page], queryFn: () => apiPage<AuditLog>('/admin/audit-logs', { ...filters, page }) })

  return (
    <>
      <PageHeader title="Audit log" description="A permanent record of who changed what: price changes, approvals, refunds, role grants, check-ins and more." />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Action, e.g. ticket_type.price_changed" value={action} onChange={(e) => setAction(e.target.value)} className="w-full sm:w-72" aria-label="Action" />
          <Input placeholder="Actor email" value={actor} onChange={(e) => setActor(e.target.value)} className="w-full sm:w-56" aria-label="Actor" />
          <Input placeholder="Entity, e.g. event" value={entity} onChange={(e) => setEntity(e.target.value)} className="w-full sm:w-44" aria-label="Entity type" />
        </div>
        {logs.isLoading ? (
          <Loading />
        ) : !logs.data?.data.length ? (
          <EmptyState title="No matching entries" />
        ) : (
          <ul className="divide-y divide-zinc-100">{logs.data.data.map((l) => <Row key={l.id} log={l} />)}</ul>
        )}
        <Pagination meta={logs.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
    </>
  )
}
