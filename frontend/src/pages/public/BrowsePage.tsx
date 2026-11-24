import { useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { CalendarSearch, SlidersHorizontal, Sparkles, X } from 'lucide-react'
import { api, apiPage } from '../../lib/api'
import { rangeFor, whenOptions, type When } from '../../lib/dates'
import { useLocations, withEvents } from '../../lib/locations'
import type { Category, PublicEvent } from '../../lib/types'
import { Button, cx, EmptyState } from '../../components/ui'
import { CardSkeleton, EventCard } from '../../components/events'

const sorts = [
  { value: '', label: 'Soonest' },
  { value: 'popular', label: 'Most popular' },
  { value: 'price', label: 'Lowest price' },
]

function FilterGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <fieldset className="border-b border-zinc-200 py-5 first:pt-0 last:border-0">
      <legend className="mb-3 text-sm font-semibold text-ink-900">{title}</legend>
      <div className="space-y-1">{children}</div>
    </fieldset>
  )
}

function Option({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <button
      onClick={onClick}
      aria-pressed={active}
      className={cx('flex w-full items-center justify-between rounded-lg px-3 py-2 text-left text-sm', active ? 'bg-brand-50 font-semibold text-brand-800' : 'text-zinc-700 hover:bg-zinc-100')}
    >
      {children}
    </button>
  )
}

/** "21 Nov" or "21 Nov to 23 Nov", for custom date filters. */
function dayRange(from?: string, to?: string): string {
  const day = (iso?: string) => (iso ? new Date(`${iso}T00:00:00`).toLocaleDateString('en-ZA', { day: 'numeric', month: 'short' }) : '')
  if (!from) return 'Custom dates'
  return !to || to === from ? day(from) : `${day(from)} to ${day(to)}`
}

export function BrowsePage() {
  const [params, setParams] = useSearchParams()
  const [filtersOpen, setFiltersOpen] = useState(false)
  const q = params.get('q') ?? ''
  const category = params.get('category') ?? ''
  const city = params.get('city') ?? ''
  const province = params.get('province') ?? ''
  const sort = params.get('sort') ?? ''
  const maxPrice = params.get('max_price') ?? ''
  // The words typed into a smart search, kept to show how they were understood.
  const asked = params.get('asked') ?? ''
  const when = (params.get('when') ?? '') as When
  const range = when === 'custom' ? { from: params.get('from') ?? undefined, to: params.get('to') ?? undefined } : rangeFor(when)

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(params)
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setParams(next, { replace: true })
  }

  const categories = useQuery({ queryKey: ['public', 'categories'], queryFn: () => api<Category[]>('/public/categories'), staleTime: 300_000 })
  const locations = useLocations()
  const provinces = withEvents(locations.data)
  // Only a province is chosen, so an empty list means the province has nothing on yet.
  const provinceOnly = !!province && !city && !q && !category && !maxPrice && !when
  const query = { q, category, city, province, sort, max_price: maxPrice, from: range.from, to: range.to }
  const events = useInfiniteQuery({
    queryKey: ['public', 'events', 'browse', query],
    queryFn: ({ pageParam }) => apiPage<PublicEvent>('/public/events', { ...query, page: pageParam, per_page: 12 }),
    initialPageParam: 1,
    getNextPageParam: (last) => (last.meta.page * last.meta.per_page < last.meta.total ? last.meta.page + 1 : undefined),
  })
  const list = events.data?.pages.flatMap((p) => p.data) ?? []
  const total = events.data?.pages[0]?.meta.total ?? 0
  const categoryName = categories.data?.find((c) => c.slug === category)?.name

  const heading = asked ? `Results for “${asked}”` : q ? `Results for “${q}”` : categoryName ?? (when === 'weekend' ? 'This weekend' : 'All events')
  const active = [
    q && { key: 'q', label: `“${q}”` },
    categoryName && { key: 'category', label: categoryName },
    province && { key: 'province', label: province },
    city && { key: 'city', label: city },
    when && { key: 'when', label: whenOptions.find((w) => w.value === when)?.label ?? dayRange(range.from, range.to) },
    maxPrice && { key: 'max_price', label: `Up to R${Number(maxPrice).toLocaleString('en-ZA')}` },
  ].filter(Boolean) as { key: string; label: string }[]

  const filters = (
    <div>
      <FilterGroup title="Date">
        {whenOptions.map((w) => (
          <Option key={w.value} active={when === w.value} onClick={() => set({ when: w.value, from: '', to: '' })}>
            {w.label}
          </Option>
        ))}
        <div className="grid gap-2 px-1 pt-2">
          <label className="text-xs text-zinc-500">
            From
            <input
              type="date"
              value={when === 'custom' ? (params.get('from') ?? '') : ''}
              onChange={(e) => set({ when: 'custom', from: e.target.value })}
              className="mt-1 block w-full rounded-lg border-0 px-2 py-1.5 text-sm text-zinc-900 ring-1 ring-zinc-300 focus:ring-2 focus:ring-brand-500"
            />
          </label>
          <label className="text-xs text-zinc-500">
            To
            <input
              type="date"
              value={when === 'custom' ? (params.get('to') ?? '') : ''}
              onChange={(e) => set({ when: 'custom', to: e.target.value })}
              className="mt-1 block w-full rounded-lg border-0 px-2 py-1.5 text-sm text-zinc-900 ring-1 ring-zinc-300 focus:ring-2 focus:ring-brand-500"
            />
          </label>
        </div>
      </FilterGroup>
      <FilterGroup title="Category">
        <Option active={!category} onClick={() => set({ category: '' })}>All categories</Option>
        {categories.data?.filter((c) => c.event_count > 0 || c.slug === category).map((c) => (
          <Option key={c.id} active={category === c.slug} onClick={() => set({ category: c.slug })}>
            {c.name} <span className="text-xs font-normal text-zinc-400">{c.event_count}</span>
          </Option>
        ))}
      </FilterGroup>
      <FilterGroup title="Location">
        <Option active={!city && !province} onClick={() => set({ city: '', province: '' })}>All provinces</Option>
        {provinces.map((p) => (
          <div key={p.name}>
            <Option active={province === p.name && !city} onClick={() => set({ province: p.name, city: '' })}>
              {p.name} <span className="text-xs font-normal text-zinc-400">{p.event_count}</span>
            </Option>
            <div className="pl-4">
              {p.cities.map((c) => (
                <Option key={c.name} active={city === c.name} onClick={() => set({ city: c.name, province: '' })}>
                  <span className="font-normal">{c.name}</span> <span className="text-xs font-normal text-zinc-400">{c.event_count}</span>
                </Option>
              ))}
            </div>
          </div>
        ))}
      </FilterGroup>
    </div>
  )

  return (
    <div className="mx-auto max-w-7xl px-4 py-8 sm:px-6 sm:py-10">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold text-ink-900 sm:text-4xl">{heading}</h1>
          <p className="mt-1 text-zinc-500">{events.isLoading ? 'Finding events…' : `${total} ${total === 1 ? 'event' : 'events'}${city || province ? ` in ${city || province}` : ''}`}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="secondary" className="lg:hidden" icon={<SlidersHorizontal className="size-4" />} onClick={() => setFiltersOpen(true)}>
            Filters
          </Button>
          <label className="flex items-center gap-2 text-sm text-zinc-500">
            Sort by
            <select value={sort} onChange={(e) => set({ sort: e.target.value })} className="h-10 rounded-full border-0 bg-white pl-4 pr-9 text-sm font-medium text-ink-900 ring-1 ring-zinc-200 focus:ring-2 focus:ring-brand-500">
              {sorts.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
          </label>
        </div>
      </div>

      {asked && (
        <p className="mt-4 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-zinc-600">
          <Sparkles className="size-4 text-brand-600" />
          <span>Smart search turned your words into the filters below.</span>
          <button onClick={() => setParams({ q: asked }, { replace: true })} className="font-medium text-brand-700 hover:text-brand-800">
            Search the exact words instead
          </button>
        </p>
      )}

      {active.length > 0 && (
        <div className="mt-4 flex flex-wrap items-center gap-2">
          {active.map((a) => (
            <button
              key={a.key}
              onClick={() => set(a.key === 'when' ? { when: '', from: '', to: '' } : { [a.key]: '' })}
              className="flex items-center gap-1.5 rounded-full bg-ink-900 py-1.5 pl-3 pr-2 text-sm text-white hover:bg-ink-800"
              aria-label={`Remove filter ${a.label}`}
            >
              {a.label} <X className="size-3.5" />
            </button>
          ))}
          <button onClick={() => setParams({}, { replace: true })} className="px-2 text-sm font-medium text-brand-700 hover:text-brand-800">
            Clear all
          </button>
        </div>
      )}

      <div className="mt-8 grid gap-8 lg:grid-cols-[240px_1fr]">
        <aside className="hidden lg:block">{filters}</aside>
        <div>
          {events.isLoading ? (
            <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">{[0, 1, 2, 3, 4, 5].map((i) => <CardSkeleton key={i} />)}</div>
          ) : list.length === 0 ? (
            <div className="rounded-3xl bg-white ring-1 ring-zinc-200">
              {provinceOnly ? (
                <EmptyState
                  icon={<CalendarSearch className="size-6" />}
                  title={`No events in ${province} yet`}
                  description="Organisers add new events all the time. Meanwhile, see what's on in the rest of the country."
                  action={<Button variant="secondary" onClick={() => setParams({}, { replace: true })}>See all events</Button>}
                />
              ) : (
                <EmptyState
                  icon={<CalendarSearch className="size-6" />}
                  title="No events match these filters"
                  description="Try another date, city or category, or clear the filters to see everything that's on."
                  action={<Button variant="secondary" onClick={() => setParams({}, { replace: true })}>Clear filters</Button>}
                />
              )}
            </div>
          ) : (
            <>
              <div className="grid gap-5 sm:grid-cols-2 xl:grid-cols-3">
                {list.map((e) => <EventCard key={e.id} event={e} />)}
              </div>
              {events.hasNextPage && (
                <div className="mt-10 text-center">
                  <Button variant="secondary" size="lg" onClick={() => events.fetchNextPage()} loading={events.isFetchingNextPage}>
                    Show more events
                  </Button>
                </div>
              )}
            </>
          )}
        </div>
      </div>

      {filtersOpen && (
        <div className="fixed inset-0 z-50 bg-ink-950/40 lg:hidden" onClick={() => setFiltersOpen(false)}>
          <div className="absolute inset-x-0 bottom-0 max-h-[85vh] overflow-y-auto rounded-t-3xl bg-white p-6" onClick={(e) => e.stopPropagation()}>
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-xl font-bold">Filters</h2>
              <button onClick={() => setFiltersOpen(false)} className="rounded-full p-2 hover:bg-zinc-100" aria-label="Close filters"><X className="size-5" /></button>
            </div>
            {filters}
            <Button size="lg" className="mt-4 w-full" onClick={() => setFiltersOpen(false)}>Show {total} {total === 1 ? 'event' : 'events'}</Button>
          </div>
        </div>
      )}
    </div>
  )
}
