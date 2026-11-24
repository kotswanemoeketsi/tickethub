import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, Navigate, useNavigate, useParams } from 'react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Check, Clock, CreditCard, Landmark, Lock } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { countdown, formatEventDate, formatMoney } from '../../lib/format'
import { useNow } from '../../lib/hooks'
import type { CheckoutResult, Order, PaymentOptions } from '../../lib/types'
import { Alert, Button, cx, EmptyState, Input, Loading } from '../../components/ui'
import { useToast } from '../../components/toast'
import { EventImage } from '../../components/events'

interface AttendeeForm {
  order_item_id: number
  label: string
  first_name: string
  last_name: string
  email: string
  phone: string
}

/** Where the buyer is in the purchase: tickets chosen, details, payment. */
function Steps({ current }: { current: 1 | 2 }) {
  const steps = ['Tickets', 'Your details', 'Payment']
  return (
    <ol className="flex items-center gap-3 text-sm">
      {steps.map((s, i) => {
        const done = i < current
        const active = i === current
        return (
          <li key={s} className="flex items-center gap-3">
            <span className={cx('flex items-center gap-2 font-medium', active ? 'text-ink-900' : done ? 'text-brand-700' : 'text-zinc-400')}>
              <span className={cx('flex size-6 items-center justify-center rounded-full text-xs font-semibold', done ? 'bg-brand-600 text-white' : active ? 'bg-ink-900 text-white' : 'bg-zinc-200 text-zinc-500')}>
                {done ? <Check className="size-3.5" /> : i + 1}
              </span>
              <span className="hidden sm:inline">{s}</span>
            </span>
            {i < steps.length - 1 && <span className="h-px w-6 bg-zinc-300 sm:w-10" />}
          </li>
        )
      })}
    </ol>
  )
}

function MethodOption({ selected, onSelect, icon, title, text }: { selected: boolean; onSelect: () => void; icon: ReactNode; title: string; text: string }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      className={cx('flex items-start gap-3 rounded-2xl border-2 p-4 text-left transition-colors', selected ? 'border-brand-600 bg-brand-50/50' : 'border-zinc-200 hover:border-zinc-300')}
    >
      <span className={cx('mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border-2', selected ? 'border-brand-600' : 'border-zinc-300')}>
        {selected && <span className="size-2.5 rounded-full bg-brand-600" />}
      </span>
      <div>
        <p className="flex items-center gap-2 font-semibold text-ink-900">{icon} {title}</p>
        <p className="mt-0.5 text-sm text-zinc-500">{text}</p>
      </div>
    </button>
  )
}

export function CheckoutPage() {
  const { orderId = '' } = useParams()
  const { me } = useAuth()
  const navigate = useNavigate()
  const toast = useToast()
  const now = useNow()
  const [attendees, setAttendees] = useState<AttendeeForm[]>([])
  const [method, setMethod] = useState<'online' | 'eft'>('online')

  const order = useQuery({ queryKey: ['order', orderId], queryFn: () => api<Order>(`/orders/${orderId}`) })
  const options = useQuery({ queryKey: ['payment-options'], queryFn: () => api<PaymentOptions>('/public/payment-options') })

  // One attendee form per ticket; the first defaults to the buyer.
  useEffect(() => {
    if (!order.data || attendees.length > 0) return
    const forms: AttendeeForm[] = []
    for (const item of order.data.items) {
      for (let i = 0; i < item.quantity; i++) {
        const first = forms.length === 0 && me
        forms.push({
          order_item_id: item.id,
          label: item.quantity > 1 ? `${item.ticket_type_name} ${i + 1}` : item.ticket_type_name,
          first_name: first ? me.user.first_name : '',
          last_name: first ? me.user.last_name : '',
          email: first ? me.user.email : '',
          phone: first ? me.user.phone : '',
        })
      }
    }
    setAttendees(forms)
  }, [order.data, attendees.length, me])

  const checkout = useMutation({
    mutationFn: () =>
      api<CheckoutResult>(`/orders/${orderId}/checkout`, {
        method: 'POST',
        body: { payment_method: method, attendees: attendees.map(({ label: _label, ...a }) => a) },
      }),
    onSuccess: (res) => {
      if (res.next === 'redirect' && res.redirect_url) {
        window.location.assign(res.redirect_url)
        return
      }
      toast.success(res.next === 'eft' ? 'Your tickets are reserved. Pay by EFT to complete your order.' : 'Your tickets are confirmed.')
      navigate(`/account/orders/${orderId}`)
    },
  })

  const cancel = useMutation({
    mutationFn: () => api(`/orders/${orderId}/cancel`, { method: 'POST' }),
    onSuccess: () => navigate(order.data ? `/events/${order.data.event.slug}` : '/'),
  })

  const errors = fieldErrors(checkout.error)
  const expired = useMemo(() => !!order.data?.expires_at && new Date(order.data.expires_at).getTime() <= now, [order.data, now])

  if (order.isLoading) return <Loading />
  if (order.error || !order.data) return <div className="py-16"><EmptyState title="Order not found" /></div>
  const o = order.data
  if (o.status !== 'pending' || o.payment_method === 'eft') return <Navigate to={`/account/orders/${o.id}`} replace />

  const update = (index: number, patch: Partial<AttendeeForm>) => setAttendees((list) => list.map((a, i) => (i === index ? { ...a, ...patch } : a)))
  const copyBuyer = () =>
    setAttendees((list) => list.map((a) => ({ ...a, first_name: a.first_name || list[0].first_name, last_name: a.last_name || list[0].last_name, email: a.email || list[0].email })))
  const remaining = o.expires_at ? new Date(o.expires_at).getTime() - now : 0

  return (
    <div className="mx-auto max-w-6xl px-4 py-8 sm:px-6 sm:py-10">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <h1 className="text-3xl font-bold text-ink-900">Checkout</h1>
        <Steps current={1} />
      </div>

      {o.expires_at && (
        <div className={cx('mt-6 flex items-center gap-3 rounded-2xl px-5 py-3.5 text-sm', expired ? 'bg-red-50 text-red-800' : remaining < 3 * 60_000 ? 'bg-marigold-200/60 text-ink-900' : 'bg-white text-zinc-700 ring-1 ring-zinc-200')}>
          <Clock className="size-5 shrink-0" />
          {expired ? (
            <span>Your reservation has expired. <Link to={`/events/${o.event.slug}`} className="font-semibold underline">Choose your tickets again</Link>.</span>
          ) : (
            <span>We're holding your tickets for <strong className="font-display text-base tabular-nums text-ink-900">{countdown(o.expires_at, now)}</strong>. Complete checkout before the timer runs out.</span>
          )}
        </div>
      )}

      <div className="mt-6 grid gap-8 lg:grid-cols-[1fr_380px]">
        <div className="space-y-6">
          <section className="rounded-3xl bg-white p-6 ring-1 ring-zinc-200 sm:p-8">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h2 className="text-xl font-bold text-ink-900">Who's going?</h2>
                <p className="mt-1 text-sm text-zinc-500">Each ticket is issued in the attendee's name and emailed to them.</p>
              </div>
              {attendees.length > 1 && <Button variant="ghost" size="sm" onClick={copyBuyer}>Use my details for the rest</Button>}
            </div>
            {errors.attendees && <div className="mt-4"><Alert tone="error">{errors.attendees}</Alert></div>}
            <div className="mt-6 space-y-6">
              {attendees.map((a, i) => (
                <div key={i} className={cx(i > 0 && 'border-t border-zinc-100 pt-6')}>
                <fieldset>
                  <legend className="mb-3 text-sm font-semibold text-ink-900">
                    Ticket {i + 1}: <span className="font-normal text-zinc-500">{a.label}</span>
                  </legend>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <Input label="First name" autoComplete={i === 0 ? 'given-name' : 'off'} value={a.first_name} onChange={(e) => update(i, { first_name: e.target.value })} error={errors[`attendees.${i}.first_name`]} required />
                    <Input label="Last name" autoComplete={i === 0 ? 'family-name' : 'off'} value={a.last_name} onChange={(e) => update(i, { last_name: e.target.value })} error={errors[`attendees.${i}.last_name`]} required />
                    <Input label="Email" type="email" value={a.email} onChange={(e) => update(i, { email: e.target.value })} error={errors[`attendees.${i}.email`]} required />
                    <Input label="Mobile (optional)" type="tel" value={a.phone} onChange={(e) => update(i, { phone: e.target.value })} error={errors[`attendees.${i}.phone`]} />
                  </div>
                </fieldset>
                </div>
              ))}
            </div>
          </section>

          {o.total_cents > 0 && (
            <section className="rounded-3xl bg-white p-6 ring-1 ring-zinc-200 sm:p-8">
              <h2 className="text-xl font-bold text-ink-900">How would you like to pay?</h2>
              <div className="mt-5 grid gap-3 sm:grid-cols-2">
                <MethodOption selected={method === 'online'} onSelect={() => setMethod('online')} icon={<CreditCard className="size-4 text-brand-600" />} title={options.data?.online_provider === 'ozow' ? 'Pay by Bank with Ozow' : 'Card or instant EFT'} text="Pay securely now. Your tickets are issued once payment is confirmed." />
                {options.data?.eft_enabled && (
                  <MethodOption selected={method === 'eft'} onSelect={() => setMethod('eft')} icon={<Landmark className="size-4 text-brand-600" />} title="Bank transfer (EFT)" text="Reserve now and pay from your banking app. Tickets are issued once the payment reflects." />
                )}
              </div>
              {errors.payment_method && <p className="mt-2 text-sm text-red-600">{errors.payment_method}</p>}
            </section>
          )}
        </div>

        <aside className="lg:sticky lg:top-36 lg:self-start">
          <div className="overflow-hidden rounded-3xl bg-white shadow-[0_1px_2px_rgba(21,18,58,0.06),0_20px_40px_-20px_rgba(21,18,58,0.35)]">
            <div className="aspect-[16/7] bg-ink-900"><EventImage event={{ image_url: o.event.image_url, title: o.event.title, category_name: '' }} /></div>
            <div className="p-6">
              <h2 className="text-lg font-bold leading-snug text-ink-900">{o.event.title}</h2>
              <p className="mt-1 text-sm text-zinc-500">{formatEventDate(o.event.starts_at, o.event.ends_at, o.event.timezone)}</p>
              <p className="text-sm text-zinc-500">{o.event.venue_name}</p>
              <dl className="mt-5 space-y-2 text-sm">
                {o.items.map((it) => (
                  <div key={it.id} className="flex justify-between gap-2">
                    <dt className="text-zinc-600">{it.quantity} × {it.ticket_type_name}</dt>
                    <dd className="tabular-nums text-ink-900">{formatMoney(it.unit_price_cents * it.quantity, o.currency)}</dd>
                  </div>
                ))}
                {o.discount_cents > 0 && (
                  <div className="flex justify-between text-emerald-700">
                    <dt>Promo {o.promotion_code}</dt>
                    <dd className="tabular-nums">−{formatMoney(o.discount_cents, o.currency)}</dd>
                  </div>
                )}
              </dl>
            </div>
            <div className="stub-line-h bg-zinc-50 p-6" style={{ ['--stub-bg' as string]: 'var(--color-canvas)' }}>
              <div className="flex items-baseline justify-between">
                <span className="text-sm text-zinc-500">Total</span>
                <span className="font-display text-2xl font-bold text-ink-900">{formatMoney(o.total_cents, o.currency)}</span>
              </div>
              {checkout.error && !Object.keys(errors).length && <div className="mt-4"><Alert tone="error">{errorMessage(checkout.error)}</Alert></div>}
              <Button size="lg" className="mt-4 w-full rounded-full" disabled={expired} loading={checkout.isPending} onClick={() => checkout.mutate()}>
                {o.total_cents === 0 ? 'Get my tickets' : method === 'online' ? `Pay ${formatMoney(o.total_cents, o.currency)}` : 'Reserve and pay by EFT'}
              </Button>
              <p className="mt-3 flex items-center justify-center gap-1.5 text-xs text-zinc-500"><Lock className="size-3.5" /> Payments are processed securely. Order {o.reference}</p>
              <button onClick={() => cancel.mutate()} disabled={cancel.isPending} className="mt-3 w-full text-center text-sm font-medium text-zinc-500 hover:text-ink-900">
                Cancel and release these tickets
              </button>
            </div>
          </div>
        </aside>
      </div>
    </div>
  )
}
