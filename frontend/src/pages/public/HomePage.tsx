import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ChevronLeft, ChevronRight, MapPin, RotateCcw, ShieldCheck, Smartphone, Store } from 'lucide-react'
import { api, apiPage } from '../../lib/api'
import { formatTime } from '../../lib/format'
import { useLocations, withEvents } from '../../lib/locations'
import type { Category, PublicEvent } from '../../lib/types'
import { cx } from '../../components/ui'
import { DateStub, EventImage, EventRail, priceLabel, SectionHeading } from '../../components/events'
import { NearYou } from '../../components/NearYou'

// Kept for pages that import the artwork component from here.
export { EventImage }

function countdownParts(iso: string, now: number) {
  const ms = new Date(iso).getTime() - now
  if (ms <= 0) return null
  const days = Math.floor(ms / 86_400_000)
  const hours = Math.floor((ms % 86_400_000) / 3_600_000)
  const minutes = Math.floor((ms % 3_600_000) / 60_000)
  return { days, hours, minutes }
}

function Countdown({ iso }: { iso: string }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000)
    return () => clearInterval(t)
  }, [])
  const p = countdownParts(iso, now)
  if (!p || p.days > 60) return null
  const unit = (n: number, label: string) => (
    <div className="min-w-14 rounded-xl bg-white/10 px-2.5 py-2 text-center backdrop-blur">
      <div className="font-display text-2xl font-bold tabular-nums text-white">{n}</div>
      <div className="text-xs text-white/70">{label}</div>
    </div>
  )
  return (
    <div className="flex gap-2" aria-label={`Starts in ${p.days} days, ${p.hours} hours and ${p.minutes} minutes`}>
      {unit(p.days, p.days === 1 ? 'day' : 'days')}
      {unit(p.hours, 'hrs')}
      {unit(p.minutes, 'min')}
    </div>
  )
}

/** The featured events, one at a time, over their artwork. */
function Hero() {
  const { data, isLoading } = useQuery({
    queryKey: ['public', 'events', 'featured'],
    queryFn: () => apiPage<PublicEvent>('/public/events', { sort: 'popular', per_page: 5 }),
  })
  const slides = (data?.data ?? []).filter((e) => !e.sold_out)
  const [index, setIndex] = useState(0)
  const [paused, setPaused] = useState(false)

  useEffect(() => {
    if (paused || slides.length < 2 || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
    const t = setInterval(() => setIndex((i) => (i + 1) % slides.length), 7000)
    return () => clearInterval(t)
  }, [paused, slides.length])

  if (isLoading) return <div className="h-[30rem] animate-pulse bg-ink-900 sm:h-[34rem]" />
  if (!slides.length) {
    return (
      <section className="bg-ink-900 px-4 py-20 text-center text-white">
        <h1 className="text-4xl font-bold sm:text-5xl">Live events, all in one place</h1>
        <p className="mx-auto mt-3 max-w-lg text-ink-200">New events are added every week. Check back soon.</p>
      </section>
    )
  }
  const e = slides[Math.min(index, slides.length - 1)]
  const go = (d: number) => setIndex((i) => (i + d + slides.length) % slides.length)

  return (
    <section
      className="relative isolate overflow-hidden bg-ink-950"
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      aria-roledescription="carousel"
      aria-label="Featured events"
    >
      {slides.map((s, i) => (
        <div key={s.id} className={cx('absolute inset-0 -z-10 transition-opacity duration-700', i === index ? 'opacity-100' : 'opacity-0')} aria-hidden={i !== index}>
          <EventImage event={s} eager={i === 0} />
        </div>
      ))}
      <div className="absolute inset-0 -z-10 bg-gradient-to-r from-ink-950/95 via-ink-950/70 to-ink-950/10" />
      <div className="absolute inset-x-0 bottom-0 -z-10 h-32 bg-gradient-to-t from-ink-950/80 to-transparent" />

      <div className="mx-auto flex min-h-[30rem] max-w-7xl flex-col justify-end px-4 pb-10 pt-24 sm:min-h-[34rem] sm:px-6 sm:pb-14">
        <div className="max-w-2xl" aria-live="polite">
          <p className="text-sm font-semibold text-marigold-300">{e.category_name}{e.selling_fast ? ' – selling fast' : ''}</p>
          <h1 className="mt-2 text-4xl font-bold leading-[1.05] text-white sm:text-6xl">{e.title}</h1>
          {e.summary && <p className="mt-4 max-w-xl text-lg text-white/80">{e.summary}</p>}
          <div className="mt-6 flex flex-wrap items-center gap-x-6 gap-y-3 text-white">
            <div className="flex items-center gap-3">
              <DateStub iso={e.starts_at} timeZone={e.timezone} tone="dark" className="w-auto" />
              <span className="text-sm text-white/80">{formatTime(e.starts_at, e.timezone)}–{formatTime(e.ends_at, e.timezone)}</span>
            </div>
            <span className="flex items-center gap-1.5 text-sm text-white/80">
              <MapPin className="size-4" /> {e.venue_name}, {e.venue_city}
            </span>
          </div>
          <div className="mt-8 flex flex-wrap items-center gap-6">
            <Link to={`/events/${e.slug}`} className="rounded-full bg-marigold-400 px-7 py-3.5 text-[15px] font-semibold text-ink-900 hover:bg-marigold-300">
              Get tickets
            </Link>
            <span className="text-sm font-semibold text-white">{priceLabel(e)}</span>
            <div className="hidden sm:block">
              <Countdown iso={e.starts_at} />
            </div>
          </div>
        </div>

        {slides.length > 1 && (
          <div className="mt-10 flex items-center gap-3">
            <button onClick={() => go(-1)} className="rounded-full bg-white/10 p-2 text-white hover:bg-white/20" aria-label="Previous event">
              <ChevronLeft className="size-5" />
            </button>
            <div className="flex gap-2">
              {slides.map((s, i) => (
                <button
                  key={s.id}
                  onClick={() => setIndex(i)}
                  className={cx('h-1.5 rounded-full transition-all', i === index ? 'w-8 bg-marigold-400' : 'w-4 bg-white/40 hover:bg-white/60')}
                  aria-label={`Show ${s.title}`}
                  aria-current={i === index}
                />
              ))}
            </div>
            <button onClick={() => go(1)} className="rounded-full bg-white/10 p-2 text-white hover:bg-white/20" aria-label="Next event">
              <ChevronRight className="size-5" />
            </button>
          </div>
        )}
      </div>
    </section>
  )
}

function QuickFilters() {
  const { data } = useLocations()
  const provinces = withEvents(data)
  const chip = (to: string, label: string) => (
    <Link key={to} to={to} className="whitespace-nowrap rounded-full bg-white px-4 py-2 text-sm font-medium text-ink-900 ring-1 ring-zinc-200 hover:ring-brand-400">
      {label}
    </Link>
  )
  return (
    <div className="mx-auto max-w-7xl px-4 pt-8 sm:px-6">
      <div className="flex gap-2 overflow-x-auto pb-1 [scrollbar-width:none]">
        {chip('/events?when=today', 'Today')}
        {chip('/events?when=weekend', 'This weekend')}
        {chip('/events?when=week', 'Next 7 days')}
        {chip('/events?when=month', 'Next 30 days')}
        <span className="mx-1 w-px shrink-0 bg-zinc-200" />
        {provinces.map((p) => chip(`/events?province=${encodeURIComponent(p.name)}`, p.name))}
      </div>
    </div>
  )
}

function CategoryTiles({ categories }: { categories: Category[] }) {
  const tones = ['bg-ink-900', 'bg-brand-700', 'bg-ink-700', 'bg-brand-900', 'bg-ink-800', 'bg-brand-800']
  return (
    <section className="mx-auto max-w-7xl px-4 py-8 sm:px-6">
      <SectionHeading title="Browse by category" />
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-flow-col lg:auto-cols-fr lg:grid-cols-none">
        {categories.map((c, i) => (
          <Link
            key={c.id}
            to={`/events?category=${c.slug}`}
            className={cx('group flex aspect-[4/3] flex-col justify-between rounded-2xl p-4 text-white transition-transform hover:-translate-y-0.5', tones[i % tones.length])}
          >
            <span className="self-end rounded-full bg-white/15 px-2 py-0.5 text-xs font-medium">
              {c.event_count} {c.event_count === 1 ? 'event' : 'events'}
            </span>
            <span className="font-display text-lg font-semibold leading-tight">{c.name}</span>
          </Link>
        ))}
      </div>
    </section>
  )
}

function Promises() {
  const items = [
    { icon: <ShieldCheck className="size-6" />, title: 'Secure checkout', text: 'Pay by card, instant EFT or bank transfer. Every payment is verified with the payment provider before tickets are issued.' },
    { icon: <Smartphone className="size-6" />, title: 'Tickets on your phone', text: 'Each ticket has its own QR code, emailed as a PDF and always in your account.' },
    { icon: <RotateCcw className="size-6" />, title: 'Covered if it’s cancelled', text: 'If an event is cancelled, every ticket holder is refunded in full.' },
  ]
  return (
    <section className="mx-auto max-w-7xl px-4 py-10 sm:px-6">
      <div className="grid gap-8 rounded-3xl bg-white p-8 ring-1 ring-zinc-200 md:grid-cols-3 md:p-10">
        {items.map((i) => (
          <div key={i.title} className="flex gap-4">
            <div className="flex size-12 shrink-0 items-center justify-center rounded-2xl bg-brand-50 text-brand-700">{i.icon}</div>
            <div>
              <h3 className="font-sans text-base font-semibold text-ink-900">{i.title}</h3>
              <p className="mt-1 text-sm leading-relaxed text-zinc-600">{i.text}</p>
            </div>
          </div>
        ))}
      </div>
    </section>
  )
}

function SellBand() {
  return (
    <section className="mx-auto max-w-7xl px-4 py-8 sm:px-6">
      <div className="relative overflow-hidden rounded-3xl bg-ink-900 px-8 py-12 text-white sm:px-12">
        <div className="absolute -right-24 -top-24 size-80 rounded-full bg-brand-600/40 blur-3xl" aria-hidden />
        <div className="relative grid items-center gap-8 md:grid-cols-[1.5fr_1fr]">
          <div>
            <h2 className="text-3xl font-bold sm:text-4xl">Hosting an event?</h2>
            <p className="mt-3 max-w-xl text-ink-200">
              Publish your event, sell tickets online and by EFT, and scan them at the door with your phone. You get live sales reports and pay a small commission only on tickets sold.
            </p>
          </div>
          <div className="flex flex-wrap gap-3 md:justify-end">
            <Link to="/sell" className="rounded-full bg-white px-6 py-3 text-sm font-semibold text-ink-900 hover:bg-zinc-100">How selling works</Link>
            <Link to="/organisers/apply" className="flex items-center gap-2 rounded-full border border-white/30 px-6 py-3 text-sm font-semibold text-white hover:bg-white/10">
              <Store className="size-4" /> Apply as an organiser
            </Link>
          </div>
        </div>
      </div>
    </section>
  )
}

export function HomePage() {
  const { data: categories } = useQuery({ queryKey: ['public', 'categories'], queryFn: () => api<Category[]>('/public/categories'), staleTime: 300_000 })
  const active = (categories ?? []).filter((c) => c.event_count > 0)
  return (
    <>
      <Hero />
      <QuickFilters />
      <NearYou />
      <EventRail title="Selling fast" description="The events everyone is booking right now." query={{ sort: 'popular' }} seeAll="/events?sort=popular" />
      <EventRail title="Coming up soon" query={{}} seeAll="/events" />
      {active.length > 0 && <CategoryTiles categories={active} />}
      {/* A row per category once it has enough events to fill one. */}
      {active.filter((c) => c.event_count >= 3).slice(0, 4).map((c) => (
        <EventRail key={c.id} title={c.name} query={{ category: c.slug }} seeAll={`/events?category=${c.slug}`} />
      ))}
      <Promises />
      <SellBand />
    </>
  )
}
