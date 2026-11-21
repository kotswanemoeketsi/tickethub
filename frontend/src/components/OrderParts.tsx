// Pieces shared by the customer and admin order pages.
import { useEffect, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { api, errorMessage, fieldErrors } from '../lib/api'
import { formatDateTime, formatMoney } from '../lib/format'
import type { EFTInstructions, Order, Ticket } from '../lib/types'
import { Alert, Button, Card, CardHeader, Checkbox, DescriptionList, Input, Modal, Textarea } from './ui'

export function EFTDetails({ eft }: { eft: EFTInstructions }) {
  return (
    <Card className="border-amber-200">
      <CardHeader title="Pay by EFT" description={`Pay before ${formatDateTime(eft.pay_by)} or the reservation is released.`} />
      <div className="px-5 pb-4">
        <DescriptionList
          items={[
            { label: 'Bank', value: eft.bank_name },
            { label: 'Account name', value: eft.account_name },
            { label: 'Account number', value: <span className="font-mono">{eft.account_number}</span> },
            { label: 'Branch code', value: <span className="font-mono">{eft.branch_code}</span> },
            { label: 'Reference', value: <span className="font-mono font-semibold">{eft.reference}</span> },
            { label: 'Amount', value: <strong>{formatMoney(eft.amount_cents, eft.currency)}</strong> },
          ]}
        />
        <p className="mt-3 text-sm text-zinc-500">Use the reference exactly as shown so we can match your payment. Your tickets are emailed once the payment reflects.</p>
      </div>
    </Card>
  )
}

export function OrderTotals({ order }: { order: Order }) {
  return (
    <div className="space-y-1.5 text-sm">
      {order.items.map((it) => (
        <div key={it.id} className="flex justify-between gap-2">
          <span>{it.quantity} × {it.ticket_type_name}</span>
          <span className="tabular-nums">{formatMoney(it.unit_price_cents * it.quantity, order.currency)}</span>
        </div>
      ))}
      {order.discount_cents > 0 && (
        <div className="flex justify-between text-emerald-700">
          <span>Discount {order.promotion_code && `(${order.promotion_code})`}</span>
          <span className="tabular-nums">−{formatMoney(order.discount_cents, order.currency)}</span>
        </div>
      )}
      <div className="flex justify-between border-t border-zinc-100 pt-2 font-semibold">
        <span>Total</span>
        <span className="tabular-nums">{formatMoney(order.total_cents, order.currency)}</span>
      </div>
      {order.refunded_cents > 0 && (
        <div className="flex justify-between text-purple-700">
          <span>Refunded</span>
          <span className="tabular-nums">−{formatMoney(order.refunded_cents, order.currency)}</span>
        </div>
      )}
    </div>
  )
}

/**
 * Changes the person a ticket is issued to. The server issues a new QR code, so the
 * previous holder's copy stops working, and emails the ticket to the new holder.
 */
export function AttendeeModal({ ticket, endpoint, onClose, onSaved }: { ticket: Ticket | null; endpoint: string; onClose: () => void; onSaved: () => void }) {
  const [form, setForm] = useState({ first_name: '', last_name: '', email: '', phone: '' })
  useEffect(() => {
    if (ticket) setForm({ first_name: ticket.holder_first_name, last_name: ticket.holder_last_name, email: ticket.holder_email, phone: '' })
  }, [ticket])
  const save = useMutation({
    mutationFn: () => api(`${endpoint}/${ticket!.id}/attendee`, { method: 'PUT', body: form }),
    onSuccess: () => {
      onSaved()
      onClose()
    },
  })
  const errors = Object.fromEntries(Object.entries(fieldErrors(save.error)).map(([k, v]) => [k.replace(/^attendee\./, ''), v]))
  return (
    <Modal
      open={!!ticket}
      onClose={onClose}
      title="Change ticket holder"
      description={ticket ? `${ticket.ticket_type_name}, ${ticket.display_code}` : undefined}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>Save and reissue</Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="info">A new QR code is issued and emailed to the holder. The old QR code stops working immediately.</Alert>
        {save.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(save.error)}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="First name" value={form.first_name} onChange={(e) => setForm({ ...form, first_name: e.target.value })} error={errors.first_name} />
          <Input label="Last name" value={form.last_name} onChange={(e) => setForm({ ...form, last_name: e.target.value })} error={errors.last_name} />
        </div>
        <Input label="Email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} />
        <Input label="Phone (optional)" type="tel" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} error={errors.phone} />
      </div>
    </Modal>
  )
}

/** Picks tickets to refund. Used by customers (request) and staff (create). Mount it to open it. */
export function RefundModal({ order, endpoint, title, submitLabel, onClose, onDone }: {
  order: Order
  endpoint: string
  title: string
  submitLabel: string
  onClose: () => void
  onDone: () => void
}) {
  const refundable = order.tickets.filter((t) => t.status === 'valid' && t.checkin_state === 'not_checked_in')
  const [selected, setSelected] = useState<number[]>(() => refundable.map((t) => t.id))
  const [reason, setReason] = useState('')
  const submit = useMutation({
    mutationFn: () => api(endpoint, { method: 'POST', body: { ticket_ids: selected, reason } }),
    onSuccess: () => {
      onDone()
      onClose()
    },
  })
  const errors = fieldErrors(submit.error)
  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button variant="danger" disabled={selected.length === 0} loading={submit.isPending} onClick={() => submit.mutate()}>
            {submitLabel} ({selected.length} ticket{selected.length === 1 ? '' : 's'})
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {submit.error && <Alert tone="error">{errors.ticket_ids || errorMessage(submit.error)}</Alert>}
        {refundable.length === 0 ? (
          <p className="text-sm text-zinc-500">There are no tickets on this order that can be refunded.</p>
        ) : (
          <div className="space-y-3">
            {refundable.map((t) => (
              <Checkbox
                key={t.id}
                checked={selected.includes(t.id)}
                onChange={(on) => setSelected((s) => (on ? [...s, t.id] : s.filter((id) => id !== t.id)))}
                label={`${t.ticket_type_name}, ${t.display_code}`}
                description={`${t.holder_first_name} ${t.holder_last_name}, ${formatMoney(t.price_cents, t.currency)}`}
              />
            ))}
          </div>
        )}
        <Textarea label="Reason" value={reason} onChange={(e) => setReason(e.target.value)} error={errors.reason} rows={3} />
        <p className="text-xs text-zinc-500">Refunded tickets are cancelled immediately and cannot be used for entry. The amount refunded follows what was paid for each ticket, after any discount.</p>
      </div>
    </Modal>
  )
}
