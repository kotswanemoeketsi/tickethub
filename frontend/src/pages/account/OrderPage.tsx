import { useState } from 'react'
import { useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Download, QrCode, UserPen } from 'lucide-react'
import { api, download, errorMessage } from '../../lib/api'
import { formatDateTime, formatEventDate, formatMoney, fullName } from '../../lib/format'
import type { Order, Ticket } from '../../lib/types'
import { Alert, Badge, Button, ButtonLink, Card, CardHeader, EmptyState, Loading, Modal, PageHeader, StatusBadge } from '../../components/ui'
import { AttendeeModal, EFTDetails, OrderTotals, RefundModal } from '../../components/OrderParts'
import { QRCode } from '../../components/QRCode'
import { useToast } from '../../components/toast'

export function OrderPage() {
  const { orderId = '' } = useParams()
  const toast = useToast()
  const queryClient = useQueryClient()
  const [showQR, setShowQR] = useState<Ticket | null>(null)
  const [editing, setEditing] = useState<Ticket | null>(null)
  const [refunding, setRefunding] = useState(false)

  const key = ['order', orderId]
  const order = useQuery({ queryKey: key, queryFn: () => api<Order>(`/orders/${orderId}`) })
  const refresh = () => queryClient.invalidateQueries({ queryKey: key })
  const cancel = useMutation({
    mutationFn: () => api(`/orders/${orderId}/cancel`, { method: 'POST' }),
    onSuccess: () => {
      toast.success('Order cancelled and tickets released.')
      refresh()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  if (order.isLoading) return <Loading />
  if (!order.data) return <EmptyState title="Order not found" />
  const o = order.data
  const pdf = (ticketId?: number) =>
    download(`/orders/${o.id}/tickets.pdf`, ticketId ? { ticket_id: ticketId } : {}, `${o.reference}.pdf`).catch((e) => toast.error(errorMessage(e)))
  const hasTickets = o.tickets.some((t) => t.status === 'valid')
  const pendingOnline = o.status === 'pending' && o.payment_method !== 'eft'

  return (
    <div className="mx-auto max-w-4xl px-4 py-8">
      <PageHeader
        back={{ to: '/account/orders', label: 'My orders' }}
        title={<span className="flex items-center gap-3">Order {o.reference} <StatusBadge status={o.status} /></span>}
        description={`Placed ${formatDateTime(o.created_at)}`}
        actions={
          <>
            {hasTickets && <Button variant="secondary" icon={<Download className="size-4" />} onClick={() => pdf()}>Download tickets</Button>}
            {o.can_request_refund && <Button variant="secondary" onClick={() => setRefunding(true)}>Request a refund</Button>}
          </>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_300px]">
        <div className="space-y-6">
          {o.status === 'pending' && o.eft && <EFTDetails eft={o.eft} />}
          {pendingOnline && (
            <Alert tone="warning" title="Payment not completed">
              <div className="mt-2 flex flex-wrap gap-2">
                <ButtonLink to={`/checkout/${o.id}`} size="sm">Complete checkout</ButtonLink>
                <Button size="sm" variant="secondary" loading={cancel.isPending} onClick={() => cancel.mutate()}>Cancel order</Button>
              </div>
            </Alert>
          )}
          {o.status === 'pending' && o.eft && (
            <div className="flex justify-end">
              <Button size="sm" variant="ghost" loading={cancel.isPending} onClick={() => cancel.mutate()}>Cancel this order</Button>
            </div>
          )}

          <Card>
            <CardHeader title={o.event.title} description={`${formatEventDate(o.event.starts_at, o.event.ends_at, o.event.timezone)}, ${o.event.venue_name}`} />
            {o.tickets.length === 0 ? (
              <p className="px-5 py-6 text-sm text-zinc-500">Tickets are issued once payment is confirmed.</p>
            ) : (
              <ul className="divide-y divide-zinc-100">
                {o.tickets.map((t) => (
                  <li key={t.id} className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
                    <div>
                      <p className="font-medium">{t.ticket_type_name} {t.is_complimentary && <Badge tone="purple">Complimentary</Badge>}</p>
                      <p className="text-sm text-zinc-500">{fullName(t.holder_first_name, t.holder_last_name)}, {t.holder_email}</p>
                      <p className="font-mono text-xs text-zinc-400">{t.display_code}</p>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      {t.status !== 'valid' ? <StatusBadge status={t.status} /> : t.checkin_state !== 'not_checked_in' && <StatusBadge status={t.checkin_state} />}
                      {t.status === 'valid' && (
                        <>
                          {t.checkin_state === 'not_checked_in' && (
                            <Button size="sm" variant="ghost" icon={<UserPen className="size-4" />} onClick={() => setEditing(t)}>Change holder</Button>
                          )}
                          <Button size="sm" variant="secondary" icon={<Download className="size-4" />} onClick={() => pdf(t.id)}>PDF</Button>
                          {t.qr_payload && <Button size="sm" icon={<QrCode className="size-4" />} onClick={() => setShowQR(t)}>QR</Button>}
                        </>
                      )}
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          {o.refunds.length > 0 && (
            <Card>
              <CardHeader title="Refunds" />
              <ul className="divide-y divide-zinc-100">
                {o.refunds.map((r) => (
                  <li key={r.id} className="flex items-center justify-between gap-3 px-5 py-3 text-sm">
                    <div>
                      <p className="font-medium">{formatMoney(r.amount_cents, r.currency)}, {r.ticket_ids.length} ticket{r.ticket_ids.length === 1 ? '' : 's'}</p>
                      <p className="text-zinc-500">{r.reference}, requested {formatDateTime(r.created_at)}</p>
                      {r.review_note && <p className="text-zinc-500">Note: {r.review_note}</p>}
                    </div>
                    <StatusBadge status={r.status} />
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>

        <aside>
          <Card className="p-5">
            <h2 className="mb-3 font-semibold">Summary</h2>
            <OrderTotals order={o} />
            {o.paid_at && <p className="mt-4 text-xs text-zinc-500">Paid {formatDateTime(o.paid_at)}{o.payment_method && ` by ${o.payment_method === 'eft' ? 'EFT' : 'online payment'}`}</p>}
            {o.event.refunds_allowed ? (
              <p className="mt-2 text-xs text-zinc-500">Refunds can be requested up to {o.event.refund_cutoff_hours} hours before the event.</p>
            ) : (
              <p className="mt-2 text-xs text-zinc-500">This event does not offer refunds unless it is cancelled.</p>
            )}
          </Card>
        </aside>
      </div>

      <Modal open={!!showQR} onClose={() => setShowQR(null)} title={showQR?.ticket_type_name} description={o.event.title} size="sm">
        {showQR?.qr_payload && (
          <div className="flex flex-col items-center text-center">
            <QRCode value={showQR.qr_payload} size={260} />
            <p className="mt-3 font-mono text-lg font-semibold tracking-widest">{showQR.display_code}</p>
            <p className="text-sm text-zinc-500">{fullName(showQR.holder_first_name, showQR.holder_last_name)}</p>
          </div>
        )}
      </Modal>
      <AttendeeModal ticket={editing} endpoint="/tickets" onClose={() => setEditing(null)} onSaved={() => { toast.success('Ticket reissued to the new holder.'); refresh() }} />
      {refunding && (
        <RefundModal
          order={o}
          endpoint={`/orders/${o.id}/refunds`}
          title="Request a refund"
          submitLabel="Request refund"
          onClose={() => setRefunding(false)}
          onDone={() => { toast.success('Refund requested. We will email you when it is processed.'); refresh() }}
        />
      )}
    </div>
  )
}
