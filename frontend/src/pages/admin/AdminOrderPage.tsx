import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Download, Mail, RotateCcw, UserPen } from 'lucide-react'
import { api, download, errorMessage } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatEventDate, formatMoney, fullName, humanize } from '../../lib/format'
import { P } from '../../lib/permissions'
import type { Order, Ticket } from '../../lib/types'
import { Badge, Button, Card, CardHeader, DescriptionList, EmptyState, Loading, PageHeader, StatusBadge } from '../../components/ui'
import { AttendeeModal, EFTDetails, OrderTotals, RefundModal } from '../../components/OrderParts'
import { useToast } from '../../components/toast'

export default function AdminOrderPage() {
  const { orderId = '' } = useParams()
  const { canFor } = useAuth()
  const toast = useToast()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<Ticket | null>(null)
  const [refunding, setRefunding] = useState(false)
  const key = ['admin', 'order', orderId]
  const order = useQuery({ queryKey: key, queryFn: () => api<Order>(`/admin/orders/${orderId}`) })
  const refresh = () => queryClient.invalidateQueries({ queryKey: key })

  const resend = useMutation({
    mutationFn: () => api<{ message: string }>(`/admin/orders/${orderId}/resend`, { method: 'POST' }),
    onSuccess: (r) => toast.success(r.message),
    onError: (e) => toast.error(errorMessage(e)),
  })
  const cancel = useMutation({
    mutationFn: () => api(`/admin/orders/${orderId}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Order cancelled and tickets released.')
      refresh()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  if (order.isLoading) return <Loading />
  if (!order.data) return <EmptyState title="Order not found" />
  const o = order.data
  const can = (perm: string) => canFor(o.event.organiser_id, perm)
  const issued = o.tickets.some((t) => t.status === 'valid')
  const pdf = () => download(`/admin/orders/${o.id}/tickets.pdf`, {}, `${o.reference}.pdf`).catch((e) => toast.error(errorMessage(e)))

  return (
    <>
      <PageHeader
        back={{ to: '/admin/orders', label: 'Orders' }}
        title={<span className="flex items-center gap-3">Order {o.reference} <StatusBadge status={o.status} />{o.source === 'complimentary' && <Badge tone="purple">Complimentary</Badge>}</span>}
        description={`Placed ${formatDateTime(o.created_at)}`}
        actions={
          <>
            {issued && <Button variant="secondary" icon={<Download className="size-4" />} onClick={pdf}>Tickets PDF</Button>}
            {issued && can(P.OrdersManage) && <Button variant="secondary" icon={<Mail className="size-4" />} loading={resend.isPending} onClick={() => resend.mutate()}>Resend tickets</Button>}
            {issued && can(P.RefundsManage) && o.source !== 'complimentary' && <Button variant="secondary" icon={<RotateCcw className="size-4" />} onClick={() => setRefunding(true)}>Refund</Button>}
            {o.status === 'pending' && can(P.OrdersManage) && (
              <Button variant="danger" icon={<Ban className="size-4" />} loading={cancel.isPending} onClick={() => window.confirm('Cancel this order and release its tickets?') && cancel.mutate()}>Cancel order</Button>
            )}
          </>
        }
      />

      <div className="grid gap-6 xl:grid-cols-[1fr_340px]">
        <div className="space-y-6">
          {o.status === 'pending' && o.eft && <EFTDetails eft={o.eft} />}
          <Card>
            <CardHeader
              title={<Link to={`/admin/events/${o.event.id}`} className="hover:text-brand-700">{o.event.title}</Link>}
              description={`${formatEventDate(o.event.starts_at, o.event.ends_at, o.event.timezone)}, ${o.event.venue_name}`}
            />
            {o.tickets.length === 0 ? (
              <p className="px-5 py-6 text-sm text-zinc-500">No tickets issued yet.</p>
            ) : (
              <ul className="divide-y divide-zinc-100">
                {o.tickets.map((t) => (
                  <li key={t.id} className="flex flex-wrap items-center justify-between gap-3 px-5 py-3">
                    <div>
                      <p className="font-medium">{t.ticket_type_name}, <span className="font-mono text-sm">{t.display_code}</span></p>
                      <p className="text-sm text-zinc-500">{fullName(t.holder_first_name, t.holder_last_name)}, {t.holder_email}</p>
                      {t.first_checked_in_at && <p className="text-xs text-zinc-500">First scanned {formatDateTime(t.first_checked_in_at, o.event.timezone)}</p>}
                    </div>
                    <div className="flex items-center gap-2">
                      <StatusBadge status={t.status !== 'valid' ? t.status : t.checkin_state} />
                      {t.status === 'valid' && can(P.OrdersManage) && t.checkin_state === 'not_checked_in' && (
                        <Button size="sm" variant="ghost" icon={<UserPen className="size-4" />} onClick={() => setEditing(t)}>Change holder</Button>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          {o.payments.length > 0 && (
            <Card>
              <CardHeader title="Payments" />
              <ul className="divide-y divide-zinc-100">
                {o.payments.map((p) => (
                  <li key={p.id} className="flex flex-wrap items-center justify-between gap-2 px-5 py-3 text-sm">
                    <div>
                      <p className="font-medium">{formatMoney(p.amount_cents, p.currency)}, {p.method === 'eft' ? 'EFT' : humanize(p.provider)}</p>
                      <p className="font-mono text-xs text-zinc-500">{p.provider_reference}{p.transaction_id && `, ${p.transaction_id}`}</p>
                      {p.failure_reason && <p className="text-xs text-red-600">{p.failure_reason}</p>}
                      {p.confirmation_note && <p className="text-xs text-zinc-500">{p.confirmation_note}</p>}
                    </div>
                    <div className="text-right">
                      <StatusBadge status={p.status} />
                      <p className="mt-0.5 text-xs text-zinc-500">{formatDateTime(p.paid_at ?? p.created_at)}</p>
                      {p.refunded_cents > 0 && <p className="text-xs text-purple-700">{formatMoney(p.refunded_cents, p.currency)} refunded</p>}
                    </div>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          {o.refunds.length > 0 && (
            <Card>
              <CardHeader title="Refunds" actions={<Link to="/admin/refunds" className="text-sm text-brand-700">Manage refunds</Link>} />
              <ul className="divide-y divide-zinc-100">
                {o.refunds.map((r) => (
                  <li key={r.id} className="flex items-center justify-between gap-3 px-5 py-3 text-sm">
                    <div>
                      <p className="font-medium">{formatMoney(r.amount_cents, r.currency)}, {r.ticket_ids.length} ticket{r.ticket_ids.length === 1 ? '' : 's'}, {humanize(r.initiated_by)}</p>
                      <p className="text-zinc-500">{r.reference}, {r.reason}</p>
                    </div>
                    <StatusBadge status={r.status} />
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>

        <div className="space-y-6">
          <Card className="p-5">
            <h2 className="mb-3 font-semibold">Summary</h2>
            <OrderTotals order={o} />
          </Card>
          <Card>
            <CardHeader title="Customer" />
            <div className="px-5 pb-3">
              <DescriptionList
                items={[
                  { label: 'Name', value: o.customer_name },
                  { label: 'Email', value: o.customer_email },
                  { label: 'Payment', value: o.payment_method ? humanize(o.payment_method) : '—' },
                  { label: 'Paid', value: formatDateTime(o.paid_at) },
                  ...(o.expires_at && o.status === 'pending' ? [{ label: 'Reserved until', value: formatDateTime(o.expires_at) }] : []),
                  ...(o.notes ? [{ label: 'Notes', value: o.notes }] : []),
                ]}
              />
            </div>
          </Card>
        </div>
      </div>

      <AttendeeModal ticket={editing} endpoint="/admin/tickets" onClose={() => setEditing(null)} onSaved={() => { toast.success('Ticket reissued.'); refresh() }} />
      {refunding && (
        <RefundModal
          order={o}
          endpoint={`/admin/orders/${o.id}/refunds`}
          title="Refund tickets"
          submitLabel="Create refund"
          onClose={() => setRefunding(false)}
          onDone={() => { toast.success('Refund created. It is processed from the refunds queue.'); refresh() }}
        />
      )}
    </>
  )
}
