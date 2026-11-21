import { useMemo, useState, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { CalendarPlus, Clock, MapPin, Minus, Navigation, Plus, Share2, ShieldCheck, Store, Tag } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatEventDate, formatMoney, formatTime } from '../../lib/format'
import type { Order, PublicEventDetail, PublicTicketType } from '../../lib/types'
import { Alert, Button, cx, EmptyState, Loading } from '../../components/ui'
import { useToast } from '../../components/toast'
import { DateStub, EventImage, EventRail, priceLabel } from '../../components/events'

function saleNote(t: PublicTicketType, timezone: string) {
  switch (t.sale_status) {
    case 'scheduled':
      return `On sale ${formatDateTime(t.sales_start_at, timezone)}`
    case 'ended':
      return 'Sales have ended'
    case 'sold_out':
      return 'Sold out'
    default:
      return t.available <= 20 ? `Only ${t.available} left` : null
  }
}

function Stepper({ value, min, max, onChange, label }: { value: number; min: number; max: number; onChange: (v: number) => void; label: string }) {
  const down = () => onChange(value - 1 < min ? 0 : value - 1)
  const up = () => onChange(value === 0 ? Math.min(min, max) : Math.min(max, value + 1))
  const btn = 'flex size-9 items-center justify-center rounded-full ring-1 ring-zinc-300 text-ink-900 hover:bg-zinc-50 disabled:opacity-30 disabled:hover:bg-transparent'
  return (
    <div className="flex items-center gap-3" role="group" aria-label={`${label} quantity`}>
      <button className={btn} onClick={down} disabled={value === 0} aria-label={`Remove one ${label} ticket`}><Minus className="size-4" /></button>
      <span className="w-5 text-center font-display text-lg font-semibold tabular-nums" aria-live="polite">{value}</span>
      <button className={btn} onClick={up} disabled={value >= max} aria-label={`Add one ${label} ticket`}><Plus className="size-4" /></button>
    </div>
  )
}

/** Downloads an .ics file so the event can be added to any calendar app. */
function addToCalendar(e: PublicEventDetail) {
  const stamp = (iso: string) => new Date(iso).toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '')
  const escape = (s: string) => s.replace(/[\\,;]/g, (m) => `\\${m}`).replace(/\n/g, '\\n')
  const location = [e.venue?.name ?? e.venue_name, e.venue?.address_line, e.venue?.city].filter(Boolean).join(', ')
  const ics = [
    'BEGIN:VCALENDAR', 'VERSION:2.0', 'PRODID:-//GotATicket//Events//EN', 'BEGIN:VEVENT',
    `UID:event-${e.id}@gotaticket`, `DTSTAMP:${stamp(new Date().toISOString())}`,
    `DTSTART:${stamp(e.starts_at)}`, `DTEND:${stamp(e.ends_at)}`,
    `SUMMARY:${escape(e.title)}`, `LOCATION:${escape(location)}`, `URL:${window.location.href}`,
    'END:VEVENT', 'END:VCALENDAR',
  ].join('\r\n')
  const url = URL.createObjectURL(new Blob([ics], { type: 'text/calendar' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `${e.slug}.ics`
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

function InfoRow({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="flex gap-4">
      <div className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-brand-50 text-brand-700">{icon}</div>
      <div className="min-w-0">
        <h3 className="font-sans text-base font-semibold text-ink-900">{title}</h3>
        <div className="mt-0.5 text-sm text-zinc-600">{children}</div>
      </div>
    </div>
  )
}

export function EventPage() {
  const { slug = '' } = useParams()
  const { me } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()
  const [quantities, setQuantities] = useState<Record<number, number>>({})
  const [promo, setPromo] = useState('')
  const [promoOpen, setPromoOpen] = useState(false)

  const { data: event, isLoading, error } = useQuery({
    queryKey: ['public', 'event', slug],
    queryFn: () => api<PublicEventDetail>(`/public/events/${slug}`),
    refetchInterval: 30_000,
  })

  const reserve = useMutation({
    mutationFn: () =>
      api<Order>('/orders', {
        method: 'POST',
        body: {
          event_id: event!.id,
          promo_code: promo.trim(),
          items: Object.entries(quantities).filter(([, q]) => q > 0).map(([id, q]) => ({ ticket_type_id: Number(id), quantity: q })),
        },
      }),
    onSuccess: (order) => navigate(`/checkout/${order.id}`),
  })

  const { count, subtotal } = useMemo(() => {
    let count = 0
    let subtotal = 0
    for (const t of event?.ticket_types ?? []) {
      const q = quantities[t.id] ?? 0
      count += q
      subtotal += q * t.price_cents
    }
    return { count, subtotal }
  }, [event, quantities])

  if (isLoading) return <Loading />
  if (error || !event) {
    return <div className="py-16"><EmptyState title="This event isn't available" description="It may have ended or been removed by the organiser." /></div>
  }

  const cancelled = event.phase === 'cancelled'
  const onSale = !cancelled && event.phase !== 'completed'
  const address = [event.venue?.address_line, event.venue?.city].filter(Boolean).join(', ')
  const mapsUrl = `https://www.google.com/maps/search/?api=1&query=${encodeURIComponent([event.venue?.name ?? event.venue_name, address].filter(Boolean).join(', '))}`

  const share = async () => {
    const data = { title: event.title, url: window.location.href }
    try {
      if (navigator.share) await navigator.share(data)
      else {
        await navigator.clipboard.writeText(data.url)
        toast.success('Link copied.')
      }
    } catch {
      // The person closed the share sheet.
    }
  }

  const checkout = () => {
    if (!me) {
      navigate(`/login?next=${encodeURIComponent(`/events/${slug}`)}`)
      return
    }
    reserve.mutate()
  }

  const promoError = fieldErrors(reserve.error).promo_code

  return (
    <>
      {/* Header: the artwork, softly repeated behind the details. */}
      <section className="relative isolate overflow-hidden bg-ink-950">
        <div className="absolute inset-0 -z-10 scale-110 opacity-50 blur-3xl">
          <EventImage event={event} eager />
        </div>
        <div className="absolute inset-0 -z-10 bg-ink-950/60" />
        <div className="mx-auto grid max-w-7xl items-center gap-8 px-4 py-8 sm:px-6 lg:grid-cols-[1.15fr_1fr] lg:py-12">
          <div className="overflow-hidden rounded-3xl shadow-2xl ring-1 ring-white/10">
            <div className="aspect-[16/9]"><EventImage event={event} eager /></div>
          </div>
          <div className="text-white">
            <p className="text-sm font-semibold text-marigold-300">{event.category_name}</p>
            <h1 className="mt-2 text-4xl font-bold leading-[1.05] sm:text-5xl">{event.title}</h1>
            {event.summary && <p className="mt-3 text-lg text-white/75">{event.summary}</p>}
            <div className="mt-6 flex items-center gap-4">
              <DateStub iso={event.starts_at} timeZone={event.timezone} tone="dark" className="w-auto" />
              <div className="border-l border-white/20 pl-4 text-sm">
                <p className="font-medium">{formatEventDate(event.starts_at, event.ends_at, event.timezone)}</p>
                <p className="mt-0.5 text-white/70">{event.venue?.name ?? event.venue_name}{event.venue?.city && `, ${event.venue.city}`}</p>
              </div>
            </div>
            <div className="mt-7 flex flex-wrap gap-2">
              <button onClick={share} className="flex items-center gap-2 rounded-full bg-white/10 px-4 py-2 text-sm font-medium hover:bg-white/20"><Share2 className="size-4" /> Share</button>
              <button onClick={() => addToCalendar(event)} className="flex items-center gap-2 rounded-full bg-white/10 px-4 py-2 text-sm font-medium hover:bg-white/20"><CalendarPlus className="size-4" /> Add to calendar</button>
            </div>
          </div>
        </div>
      </section>

      <div className="mx-auto grid max-w-7xl gap-10 px-4 py-10 sm:px-6 lg:grid-cols-[1fr_400px]">
        <div className="min-w-0 space-y-10">
          {cancelled && <Alert tone="error" title="This event has been cancelled">Ticket holders are refunded in full; there is nothing you need to do.</Alert>}

          <section>
            <h2 className="text-2xl font-bold text-ink-900">About this event</h2>
            <p className="prose-text mt-3 text-[15px] text-zinc-700">{event.description}</p>
          </section>

          <section className="grid gap-6 sm:grid-cols-2">
            <InfoRow icon={<Clock className="size-5" />} title="Date and time">
              {formatEventDate(event.starts_at, event.ends_at, event.timezone)}
              <p className="text-xs text-zinc-500">Times are in {event.timezone.replace('_', ' ')}</p>
            </InfoRow>
            <InfoRow icon={<MapPin className="size-5" />} title={event.venue?.name ?? event.venue_name}>
              {address}
              <a href={mapsUrl} target="_blank" rel="noreferrer" className="mt-1 flex items-center gap-1 font-medium text-brand-700 hover:text-brand-800">
                <Navigation className="size-3.5" /> Get directions
              </a>
            </InfoRow>
            <InfoRow icon={<ShieldCheck className="size-5" />} title="Refunds">
              {event.refunds_allowed
                ? `You can request a refund up to ${event.refund_cutoff_hours} hours before the event starts.`
                : 'Tickets are non-refundable unless the event is cancelled, in which case you are refunded in full.'}
            </InfoRow>
            <InfoRow icon={<Store className="size-5" />} title="Organiser">
              {event.organiser_name}
            </InfoRow>
          </section>
        </div>

        {/* The ticket panel, styled as a ticket: options above the tear line, total below. */}
        <aside id="tickets" className="lg:sticky lg:top-36 lg:self-start">
          <div className="overflow-hidden rounded-3xl bg-white shadow-[0_1px_2px_rgba(21,18,58,0.06),0_20px_40px_-20px_rgba(21,18,58,0.35)]">
            <div className="p-6">
              <h2 className="text-xl font-bold text-ink-900">Tickets</h2>
              <ul className="mt-2 divide-y divide-zinc-100">
                {event.ticket_types.map((t) => {
                  const note = saleNote(t, event.timezone)
                  const buyable = onSale && t.sale_status === 'on_sale'
                  return (
                    <li key={t.id} className="flex items-center justify-between gap-4 py-4">
                      <div className="min-w-0">
                        <p className={cx('font-semibold', buyable ? 'text-ink-900' : 'text-zinc-400')}>{t.name}</p>
                        {t.description && <p className="text-xs text-zinc-500">{t.description}</p>}
                        <p className={cx('mt-1 font-display text-lg font-semibold', buyable ? 'text-ink-900' : 'text-zinc-400')}>
                          {t.price_cents === 0 ? 'Free' : formatMoney(t.price_cents, event.currency)}
                        </p>
                        {note && <p className={cx('text-xs font-medium', t.sale_status === 'on_sale' ? 'text-marigold-600' : 'text-zinc-500')}>{note}</p>}
                      </div>
                      {buyable ? (
                        <Stepper
                          label={t.name}
                          value={quantities[t.id] ?? 0}
                          min={t.min_per_order}
                          max={Math.min(t.max_per_order, t.available)}
                          onChange={(v) => setQuantities((q) => ({ ...q, [t.id]: v }))}
                        />
                      ) : (
                        <span className="rounded-full bg-zinc-100 px-3 py-1 text-xs font-medium text-zinc-500">{t.sale_status === 'sold_out' ? 'Sold out' : 'Unavailable'}</span>
                      )}
                    </li>
                  )
                })}
              </ul>
              {onSale && (
                <div className="mt-2">
                  {promoOpen || promo ? (
                    <div>
                      <label htmlFor="promo" className="text-sm font-medium text-zinc-700">Promo code</label>
                      <input
                        id="promo"
                        value={promo}
                        onChange={(e) => setPromo(e.target.value.toUpperCase())}
                        className={cx('mt-1.5 h-10 w-full rounded-xl border-0 px-3 font-medium uppercase tracking-wide ring-1 ring-zinc-300 focus:ring-2 focus:ring-brand-500', promoError && 'ring-red-400')}
                        autoFocus
                      />
                      {promoError && <p className="mt-1 text-sm text-red-600">{promoError}</p>}
                    </div>
                  ) : (
                    <button onClick={() => setPromoOpen(true)} className="flex items-center gap-1.5 text-sm font-medium text-brand-700 hover:text-brand-800">
                      <Tag className="size-4" /> I have a promo code
                    </button>
                  )}
                </div>
              )}
            </div>

            {onSale && (
              <div className="stub-line-h bg-zinc-50 p-6" style={{ ['--stub-bg' as string]: 'var(--color-canvas)' }}>
                {reserve.error && !promoError && <div className="mb-4"><Alert tone="error">{errorMessage(reserve.error)}</Alert></div>}
                <div className="flex items-baseline justify-between">
                  <span className="text-sm text-zinc-500">{count === 0 ? 'Choose your tickets' : `${count} ${count === 1 ? 'ticket' : 'tickets'}`}</span>
                  <span className="font-display text-2xl font-bold text-ink-900">{formatMoney(subtotal, event.currency)}</span>
                </div>
                {promo && <p className="mt-1 text-right text-xs text-zinc-500">Discount applied at checkout</p>}
                <Button size="lg" className="mt-4 w-full rounded-full" disabled={count === 0} loading={reserve.isPending} onClick={checkout}>
                  {me ? 'Continue to checkout' : 'Sign in to continue'}
                </Button>
                <p className="mt-3 text-center text-xs text-zinc-500">Your tickets are held for 15 minutes while you check out.</p>
              </div>
            )}
          </div>
        </aside>
      </div>

      <EventRail title="More like this" query={{ category: event.category_slug, per_page: 9 }} exclude={event.id} seeAll={`/events?category=${event.category_slug}`} />

      {/* On phones the ticket panel is far down the page, so keep a shortcut in reach. */}
      {onSale && (
        <div className="sticky bottom-0 z-30 border-t border-zinc-200 bg-white/95 px-4 py-3 backdrop-blur lg:hidden">
          <div className="flex items-center justify-between gap-4">
            <div>
              <p className="text-sm font-semibold text-ink-900">{count > 0 ? `${count} selected: ${formatMoney(subtotal, event.currency)}` : priceLabel(event)}</p>
              <p className="text-xs text-zinc-500">{formatTime(event.starts_at, event.timezone)}, {event.venue_city}</p>
            </div>
            {count > 0 ? (
              <Button className="rounded-full" loading={reserve.isPending} onClick={checkout}>Checkout</Button>
            ) : (
              <a href="#tickets" className="rounded-full bg-brand-600 px-5 py-2.5 text-sm font-semibold text-white">Select tickets</a>
            )}
          </div>
        </div>
      )}
    </>
  )
}
