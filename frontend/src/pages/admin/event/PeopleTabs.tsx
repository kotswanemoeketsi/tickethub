// Attendees, orders and check-in activity for one event.
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Gift } from 'lucide-react'
import { api, apiPage, errorMessage, fieldErrors } from '../../../lib/api'
import { formatDateTime, formatMoney, fullName, percent } from '../../../lib/format'
import { useDebounced } from '../../../lib/hooks'
import type { AdminEvent, AttendeeRow, CheckinStats, OrderSummary, ScanRecord, TicketType } from '../../../lib/types'
import { Alert, Badge, Button, Card, CardHeader, Input, Modal, Pagination, Select, Stat, StatusBadge, Textarea } from '../../../components/ui'
import { DataTable, type Column } from '../../../components/DataTable'
import { useToast } from '../../../components/toast'

export function AttendeesTab({ event, canComp }: { event: AdminEvent; canComp: boolean }) {
  const [q, setQ] = useState('')
  const [state, setState] = useState('')
  const [page, setPage] = useState(1)
  const [comping, setComping] = useState(false)
  const query = useDebounced(q, 300)
  const navigate = useNavigate()
  const rows = useQuery({
    queryKey: ['admin', 'event', String(event.id), 'attendees', { query, state, page }],
    queryFn: () => apiPage<AttendeeRow>(`/admin/events/${event.id}/attendees`, { q: query, checkin_state: state, page }),
  })
  const columns: Column<AttendeeRow>[] = [
    { header: 'Attendee', cell: (a) => <div><p className="font-medium text-zinc-900">{fullName(a.first_name, a.last_name)}</p><p className="text-xs text-zinc-500">{a.email}</p></div> },
    { header: 'Ticket', cell: (a) => <div><p>{a.ticket_type_name} {a.is_complimentary && <Badge tone="purple">Comp</Badge>}</p><p className="font-mono text-xs text-zinc-500">{a.display_code}</p></div> },
    { header: 'Order', cell: (a) => <span className="font-mono">{a.order_reference}</span> },
    { header: 'Status', cell: (a) => (a.ticket_status !== 'valid' ? <StatusBadge status={a.ticket_status} /> : <StatusBadge status={a.checkin_state} />) },
    { header: 'Checked in', cell: (a) => (a.first_checked_in_at ? formatDateTime(a.first_checked_in_at, event.timezone) : '—') },
  ]
  return (
    <Card>
      <CardHeader
        title="Attendees"
        actions={canComp && event.status === 'published' && <Button size="sm" variant="secondary" icon={<Gift className="size-4" />} onClick={() => setComping(true)}>Issue complimentary tickets</Button>}
      />
      <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
        <Input placeholder="Search name, email or ticket code" value={q} onChange={(e) => { setQ(e.target.value); setPage(1) }} className="w-full sm:w-72" aria-label="Search attendees" />
        <Select
          aria-label="Check-in status"
          className="w-48"
          placeholder="Any check-in status"
          value={state}
          onChange={(e) => { setState(e.target.value); setPage(1) }}
          options={[{ value: 'not_checked_in', label: 'Not checked in' }, { value: 'checked_in', label: 'Checked in' }, { value: 'checked_out', label: 'Checked out' }]}
        />
      </div>
      <DataTable columns={columns} rows={rows.data?.data} loading={rows.isLoading} rowKey={(a) => a.ticket_id} onRowClick={(a) => navigate(`/admin/orders/${a.order_id}`)} empty={{ title: 'No attendees yet' }} />
      <Pagination meta={rows.data?.meta} onPage={setPage} />
      {comping && <ComplimentaryModal event={event} onClose={() => setComping(false)} />}
    </Card>
  )
}

function ComplimentaryModal({ event, onClose }: { event: AdminEvent; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const types = useQuery({ queryKey: ['admin', 'event', String(event.id), 'ticket-types'], queryFn: () => api<TicketType[]>(`/admin/events/${event.id}/ticket-types`) })
  const [form, setForm] = useState({ ticket_type_id: '', quantity: '1', first_name: '', last_name: '', email: '', note: '' })
  const issue = useMutation({
    mutationFn: () => api(`/admin/events/${event.id}/complimentary`, { method: 'POST', body: { ...form, ticket_type_id: Number(form.ticket_type_id) || 0, quantity: Number(form.quantity) || 0 } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'event', String(event.id)] })
      toast.success('Complimentary tickets issued and emailed.')
      onClose()
    },
  })
  const errors = fieldErrors(issue.error)
  return (
    <Modal
      open
      onClose={onClose}
      title="Issue complimentary tickets"
      description="Free tickets for guests, sponsors or media. They count against capacity."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={issue.isPending} onClick={() => issue.mutate()}>Issue tickets</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {issue.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(issue.error)}</Alert></div>}
        <Select
          label="Ticket type"
          className="sm:col-span-2"
          placeholder="Choose a ticket type"
          options={(types.data ?? []).map((t) => ({ value: t.id, label: `${t.name} (${t.quantity_available} left)` }))}
          value={form.ticket_type_id}
          onChange={(e) => setForm({ ...form, ticket_type_id: e.target.value })}
          error={errors.ticket_type_id}
        />
        <Input label="Quantity" type="number" min={1} max={50} value={form.quantity} onChange={(e) => setForm({ ...form, quantity: e.target.value })} error={errors.quantity} />
        <div />
        <Input label="Guest first name" value={form.first_name} onChange={(e) => setForm({ ...form, first_name: e.target.value })} error={errors.first_name} />
        <Input label="Guest last name" value={form.last_name} onChange={(e) => setForm({ ...form, last_name: e.target.value })} error={errors.last_name} />
        <Input label="Guest email" type="email" className="sm:col-span-2" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} />
        <Textarea label="Internal note" className="sm:col-span-2" rows={2} value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} error={errors.note} />
      </div>
    </Modal>
  )
}

export function EventOrdersTab({ event }: { event: AdminEvent }) {
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const navigate = useNavigate()
  const orders = useQuery({
    queryKey: ['admin', 'orders', { event: event.id, status, page }],
    queryFn: () => apiPage<OrderSummary>('/admin/orders', { event_id: event.id, status, page }),
  })
  return (
    <Card>
      <CardHeader
        title="Orders"
        actions={
          <Select
            aria-label="Status"
            placeholder="All statuses"
            value={status}
            onChange={(e) => { setStatus(e.target.value); setPage(1) }}
            options={['pending', 'paid', 'partially_refunded', 'refunded', 'expired', 'cancelled'].map((s) => ({ value: s, label: s.replace('_', ' ') }))}
          />
        }
      />
      <OrderTable rows={orders.data?.data} loading={orders.isLoading} onOpen={(o) => navigate(`/admin/orders/${o.id}`)} hideEvent />
      <Pagination meta={orders.data?.meta} onPage={setPage} />
    </Card>
  )
}

export function OrderTable({ rows, loading, onOpen, hideEvent }: { rows?: OrderSummary[]; loading: boolean; onOpen: (o: OrderSummary) => void; hideEvent?: boolean }) {
  const columns: Column<OrderSummary>[] = [
    { header: 'Order', cell: (o) => <div><p className="font-mono font-medium text-zinc-900">{o.reference}</p><p className="text-xs text-zinc-500">{formatDateTime(o.created_at)}</p></div> },
    { header: 'Customer', cell: (o) => <div><p className="text-zinc-900">{o.customer_name}</p><p className="text-xs text-zinc-500">{o.customer_email}</p></div> },
    ...(hideEvent ? [] : [{ header: 'Event', cell: (o: OrderSummary) => o.event_title }]),
    { header: 'Tickets', cell: (o) => o.ticket_count, align: 'right' },
    { header: 'Total', cell: (o) => formatMoney(o.total_cents, o.currency), align: 'right' },
    { header: 'Method', cell: (o) => (o.source === 'complimentary' ? <Badge tone="purple">Complimentary</Badge> : <span className="uppercase text-xs text-zinc-500">{o.payment_method ?? '—'}</span>) },
    { header: 'Status', cell: (o) => <StatusBadge status={o.status} /> },
  ]
  return <DataTable columns={columns} rows={rows} loading={loading} rowKey={(o) => o.id} onRowClick={onOpen} empty={{ title: 'No orders found' }} />
}

export function CheckinTab({ event }: { event: AdminEvent }) {
  const overview = useQuery({
    queryKey: ['admin', 'event', String(event.id), 'checkins'],
    queryFn: () => api<{ stats: CheckinStats; recent_scans: ScanRecord[] }>(`/admin/events/${event.id}/checkins`, { query: { limit: 50 } }),
    refetchInterval: 10_000,
  })
  const s = overview.data?.stats
  const columns: Column<ScanRecord>[] = [
    { header: 'Time', cell: (r) => formatDateTime(r.scanned_at, event.timezone) },
    { header: 'Ticket', cell: (r) => <div><p>{r.holder_name || '—'}</p><p className="font-mono text-xs text-zinc-500">{r.display_code || 'Unknown code'}</p></div> },
    { header: 'Direction', cell: (r) => (r.direction === 'in' ? 'Entry' : 'Exit') },
    { header: 'Result', cell: (r) => <div><StatusBadge status={r.result} />{r.reason && <p className="mt-0.5 text-xs text-zinc-500">{r.reason.replace(/_/g, ' ')}</p>}</div> },
    { header: 'Gate', cell: (r) => r.gate || '—' },
    { header: 'Scanned by', cell: (r) => r.scanned_by },
  ]
  return (
    <div className="space-y-6">
      {s && (
        <div className="grid gap-4 sm:grid-cols-4">
          <Stat label="Tickets issued" value={s.tickets_issued} />
          <Stat label="Checked in" value={s.checked_in} hint={percent(s.checked_in, s.tickets_issued)} />
          <Stat label="Inside now" value={s.currently_inside} />
          <Stat label="Rejected scans" value={s.rejected_scans} />
        </div>
      )}
      <Card>
        <CardHeader title="Recent scans" description="Refreshes every 10 seconds." />
        <DataTable columns={columns} rows={overview.data?.recent_scans} loading={overview.isLoading} rowKey={(r) => r.id} empty={{ title: 'No scans yet' }} />
      </Card>
    </div>
  )
}
