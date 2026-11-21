// Marketplace building blocks: event artwork, the date stub, event cards and card rails.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, MapPin } from 'lucide-react'
import { apiPage } from '../lib/api'
import { formatMoney, formatTime } from '../lib/format'
import type { PublicEvent } from '../lib/types'
import { cx } from './ui'

type ArtworkEvent = Pick<PublicEvent, 'image_url' | 'title' | 'category_name'>

/** Event artwork, or a quiet branded panel when the organiser has not uploaded an image. */
export function EventImage({ event, className, eager }: { event: ArtworkEvent; className?: string; eager?: boolean }) {
  if (event.image_url) {
    return <img src={event.image_url} alt="" className={cx('h-full w-full object-cover', className)} loading={eager ? 'eager' : 'lazy'} />
  }
  return (
    <div className={cx('flex h-full w-full items-end bg-ink-900 bg-[radial-gradient(circle_at_70%_20%,var(--color-brand-700),transparent_60%)] p-4', className)}>
      <span className="font-display text-lg font-semibold text-white/80">{event.category_name || 'Live event'}</span>
    </div>
  )
}

function parts(iso: string, timeZone: string) {
  const d = new Date(iso)
  const f = (o: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat('en-ZA', { timeZone, ...o }).format(d)
  return { day: f({ day: 'numeric' }), month: f({ month: 'short' }), weekday: f({ weekday: 'short' }) }
}

/** The date half of a ticket stub: weekday, a large day numeral and the month. */
export function DateStub({ iso, timeZone, tone = 'light', className }: { iso: string; timeZone: string; tone?: 'light' | 'dark'; className?: string }) {
  const p = parts(iso, timeZone)
  return (
    <div className={cx('flex w-16 shrink-0 flex-col items-center justify-center text-center leading-none', className)}>
      <span className={cx('text-xs font-medium', tone === 'dark' ? 'text-white/70' : 'text-zinc-500')}>{p.weekday}</span>
      <span className={cx('mt-1 font-display text-3xl font-bold', tone === 'dark' ? 'text-marigold-400' : 'text-ink-900')}>{p.day}</span>
      <span className={cx('mt-1 text-xs font-semibold', tone === 'dark' ? 'text-white' : 'text-brand-700')}>{p.month}</span>
    </div>
  )
}

export function priceLabel(e: Pick<PublicEvent, 'from_price_cents' | 'currency' | 'sold_out'>): string {
  if (e.sold_out) return 'Sold out'
  if (e.from_price_cents === null) return 'Tickets soon'
  if (e.from_price_cents === 0) return 'Free'
  return `From ${formatMoney(e.from_price_cents, e.currency).replace('.00', '')}`
}

/** An event card shaped like a ticket: artwork on top, the stub below. */
export function EventCard({ event, className }: { event: PublicEvent; className?: string }) {
  return (
    <Link
      to={`/events/${event.slug}`}
      className={cx('group flex flex-col overflow-hidden rounded-2xl bg-white shadow-[0_1px_2px_rgba(21,18,58,0.06),0_8px_24px_-12px_rgba(21,18,58,0.18)] transition-shadow hover:shadow-[0_1px_2px_rgba(21,18,58,0.08),0_16px_32px_-12px_rgba(21,18,58,0.3)]', className)}
    >
      <div className="relative aspect-[16/9] overflow-hidden bg-ink-900">
        <EventImage event={event} className="transition-transform duration-500 group-hover:scale-[1.04]" />
        <div className="absolute left-3 top-3 flex flex-wrap gap-1.5">
          {event.phase === 'live' && <span className="rounded-full bg-emerald-500 px-2.5 py-1 text-xs font-semibold text-white">On now</span>}
          {event.sold_out ? (
            <span className="rounded-full bg-ink-900/85 px-2.5 py-1 text-xs font-semibold text-white">Sold out</span>
          ) : (
            event.selling_fast && <span className="rounded-full bg-marigold-400 px-2.5 py-1 text-xs font-semibold text-ink-900">Selling fast</span>
          )}
        </div>
        {event.distance_km != null && (
          <span className="absolute right-3 top-3 flex items-center gap-1 rounded-full bg-white/95 px-2.5 py-1 text-xs font-semibold text-ink-900 shadow-sm">
            <MapPin className="size-3 text-brand-600" />
            {distanceLabel(event.distance_km)}
          </span>
        )}
      </div>
      {/* The tear line runs across the ticket, with a notch punched out at each edge. */}
      <div className="stub-line-h flex flex-1 border-t-0" style={{ ['--stub-bg' as string]: 'var(--color-canvas)' }}>
        <DateStub iso={event.starts_at} timeZone={event.timezone} className="my-4 border-r border-zinc-100" />
        <div className="flex min-w-0 flex-1 flex-col px-4 py-3.5">
          <h3 className="line-clamp-2 font-display text-[1.05rem] font-semibold leading-snug text-ink-900 group-hover:text-brand-700">{event.title}</h3>
          <p className="mt-1 flex items-center gap-1 truncate text-sm text-zinc-500">
            <MapPin className="size-3.5 shrink-0" />
            <span className="truncate">{event.venue_name}{event.venue_city && `, ${event.venue_city}`}</span>
          </p>
          <div className="mt-auto flex items-baseline justify-between gap-2 pt-3">
            <span className={cx('text-sm font-semibold', event.sold_out ? 'text-zinc-400' : 'text-ink-900')}>{priceLabel(event)}</span>
            <span className="text-xs text-zinc-500">{formatTime(event.starts_at, event.timezone)}</span>
          </div>
        </div>
      </div>
    </Link>
  )
}

export function CardSkeleton() {
  return (
    <div className="overflow-hidden rounded-2xl bg-white">
      <div className="aspect-[16/9] animate-pulse bg-zinc-200" />
      <div className="space-y-2 p-4">
        <div className="h-4 w-3/4 animate-pulse rounded bg-zinc-200" />
        <div className="h-3 w-1/2 animate-pulse rounded bg-zinc-100" />
      </div>
    </div>
  )
}

export function SectionHeading({ title, description, action }: { title: string; description?: ReactNode; action?: ReactNode }) {
  return (
    <div className="mb-5 flex items-end justify-between gap-4">
      <div>
        <h2 className="text-2xl font-bold text-ink-900 sm:text-[1.75rem]">{title}</h2>
        {description && <p className="mt-1 text-zinc-500">{description}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

export function distanceLabel(km: number): string {
  return km < 1 ? 'Under 1 km away' : `${Math.round(km).toLocaleString('en-ZA')} km away`
}

const reducedMotion = () => window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

/**
 * A titled carousel of events: swipe on touch screens, or page through with the arrows on
 * larger screens. With no events it shows empty, or nothing at all when empty is not given.
 */
export function EventCarousel({ title, description, action, toolbar, events, isLoading, empty }: {
  title: string
  description?: ReactNode
  action?: ReactNode
  /** Shown between the heading and the events, such as filters the buyer opened. */
  toolbar?: ReactNode
  events: PublicEvent[]
  isLoading?: boolean
  empty?: ReactNode
}) {
  const track = useRef<HTMLDivElement>(null)
  const [edges, setEdges] = useState({ start: true, end: true })
  const measure = useCallback(() => {
    const el = track.current
    if (!el) return
    const start = el.scrollLeft <= 4
    const end = el.scrollLeft + el.clientWidth >= el.scrollWidth - 4
    setEdges((prev) => (prev.start === start && prev.end === end ? prev : { start, end }))
  }, [])
  useEffect(() => {
    measure()
    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [measure, events.length, isLoading])

  const page = (direction: 1 | -1) => {
    const el = track.current
    el?.scrollBy({ left: direction * el.clientWidth, behavior: reducedMotion() ? 'auto' : 'smooth' })
  }
  const showRail = isLoading || events.length > 0
  if (!showRail && empty === undefined) return null

  const arrow = (direction: 1 | -1, disabled: boolean) => (
    <button
      type="button"
      onClick={() => page(direction)}
      disabled={disabled}
      aria-label={direction === 1 ? `Next ${title} events` : `Previous ${title} events`}
      className="grid size-9 place-items-center rounded-full bg-white text-ink-900 ring-1 ring-zinc-200 transition hover:ring-brand-400 disabled:cursor-default disabled:opacity-40 disabled:hover:ring-zinc-200"
    >
      {direction === 1 ? <ChevronRight className="size-4" /> : <ChevronLeft className="size-4" />}
    </button>
  )
  const scrollable = showRail && !(edges.start && edges.end)

  return (
    <section className="mx-auto max-w-7xl px-4 py-8 sm:px-6" aria-label={title}>
      <SectionHeading
        title={title}
        description={description}
        action={(action || scrollable) && (
          <div className="flex items-center gap-4">
            {action}
            {scrollable && <div className="hidden gap-2 sm:flex">{arrow(-1, edges.start)}{arrow(1, edges.end)}</div>}
          </div>
        )}
      />
      {toolbar}
      {showRail ? (
        <div ref={track} onScroll={measure} className="rail">
          {isLoading ? [0, 1, 2, 3].map((i) => <CardSkeleton key={i} />) : events.map((e) => <EventCard key={e.id} event={e} />)}
        </div>
      ) : (
        empty
      )}
    </section>
  )
}

/** A carousel of events from the marketplace listing. Renders nothing when there are none. */
export function EventRail({ title, description, query, seeAll, exclude }: {
  title: string
  description?: string
  query: Record<string, string | number>
  seeAll?: string
  exclude?: number
}) {
  const { data, isLoading } = useQuery({
    queryKey: ['public', 'events', 'rail', query],
    queryFn: () => apiPage<PublicEvent>('/public/events', { per_page: 8, ...query }),
  })
  const events = (data?.data ?? []).filter((e) => e.id !== exclude)
  return (
    <EventCarousel
      title={title}
      description={description}
      events={events}
      isLoading={isLoading}
      action={seeAll && <Link to={seeAll} className="text-sm font-semibold text-brand-700 hover:text-brand-800">See all</Link>}
    />
  )
}
