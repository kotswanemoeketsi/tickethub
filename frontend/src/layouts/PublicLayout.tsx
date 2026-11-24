import { useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useLocation, useNavigate, useSearchParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, CircleHelp, LayoutDashboard, LoaderCircle, LogOut, MapPin, Menu, QrCode, ReceiptText, Search, Settings, Ticket, X } from 'lucide-react'
import { api } from '../lib/api'
import { filtersToParams, useFeatures, wantsSmartSearch, type SearchFilters } from '../lib/ai'
import { useAuth } from '../lib/auth'
import { allProvinces, useLocations } from '../lib/locations'
import type { Category } from '../lib/types'
import { cx } from '../components/ui'
import { InstallApp } from '../components/InstallApp'
import { appName } from '../lib/brand'

export { appName }

export function Logo({ light = false }: { light?: boolean }) {
  return (
    <Link to="/" className="flex shrink-0 items-center gap-2.5 whitespace-nowrap" aria-label={`${appName} home`}>
      <img src="/icon.svg" alt="" className="size-9" />
      <span className={cx('font-display text-[1.35rem] font-bold tracking-tight', light ? 'text-white' : 'text-ink-900')}>{appName}</span>
    </Link>
  )
}

function useCategories() {
  return useQuery({ queryKey: ['public', 'categories'], queryFn: () => api<Category[]>('/public/categories'), staleTime: 300_000 })
}

/** Closes a popover when clicking elsewhere or pressing Escape. */
function useDismiss<T extends HTMLElement>(open: boolean, close: () => void) {
  const ref = useRef<T>(null)
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && close()
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, close])
  return ref
}

function SearchBox({ className, autoFocus, onDone }: { className?: string; autoFocus?: boolean; onDone?: () => void }) {
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const location = useLocation()
  const features = useFeatures()
  const [q, setQ] = useState(location.pathname === '/events' ? (params.get('asked') ?? params.get('q') ?? '') : '')
  const [interpreting, setInterpreting] = useState(false)
  const submit = async (e: FormEvent) => {
    e.preventDefault()
    const text = q.trim()
    if (interpreting) return
    // A sentence such as "comedy in Durban this weekend under R300" becomes filters. If
    // that fails for any reason, it is searched as plain words instead.
    if (features.ai_search && wantsSmartSearch(text)) {
      setInterpreting(true)
      try {
        const filters = await api<SearchFilters>('/public/search/interpret', { query: { q: text } })
        navigate(`/events?${filtersToParams(filters, text).toString()}`)
        onDone?.()
        return
      } catch {
        // Fall through to the keyword search.
      } finally {
        setInterpreting(false)
      }
    }
    const next = new URLSearchParams(location.pathname === '/events' ? params : undefined)
    next.delete('asked')
    if (text) next.set('q', text)
    else next.delete('q')
    navigate(`/events?${next.toString()}`)
    onDone?.()
  }
  return (
    <form onSubmit={submit} role="search" className={cx('relative', className)}>
      <Search className="pointer-events-none absolute left-3.5 top-1/2 size-[18px] -translate-y-1/2 text-zinc-400" />
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        autoFocus={autoFocus}
        placeholder={features.ai_search ? 'Try “comedy in Durban this weekend”' : 'Search artists, events, venues'}
        aria-label="Search events"
        aria-busy={interpreting}
        className="h-11 w-full rounded-full border-0 bg-zinc-100 pl-11 pr-10 text-[15px] text-zinc-900 placeholder:text-zinc-500 focus:bg-white focus:ring-2 focus:ring-brand-500"
      />
      {interpreting && (
        <LoaderCircle className="absolute right-3.5 top-1/2 size-[18px] -translate-y-1/2 animate-spin text-brand-600" aria-label="Understanding your search" />
      )}
    </form>
  )
}

/** Filters the marketplace by province, or by a city within one. */
function LocationPicker() {
  const { data } = useLocations()
  const provinces = allProvinces(data)
  const [open, setOpen] = useState(false)
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const location = useLocation()
  const onBrowse = location.pathname === '/events'
  const city = onBrowse ? (params.get('city') ?? '') : ''
  const province = onBrowse ? (params.get('province') ?? '') : ''
  const ref = useDismiss<HTMLDivElement>(open, () => setOpen(false))
  const choose = (next: { province?: string; city?: string }) => {
    const query = new URLSearchParams(onBrowse ? params : undefined)
    query.delete('province')
    query.delete('city')
    if (next.province) query.set('province', next.province)
    if (next.city) query.set('city', next.city)
    navigate(`/events?${query.toString()}`)
    setOpen(false)
  }
  const option = (label: string, selected: boolean, onClick: () => void, className: string, count?: number) => (
    <button
      role="option"
      aria-selected={selected}
      onClick={onClick}
      className={cx('flex w-full items-center justify-between gap-3 px-4 text-left hover:bg-zinc-50', className)}
    >
      <span className="truncate">{label}</span>
      {count !== undefined && <span className="shrink-0 text-xs font-normal text-zinc-400">{count > 0 ? count : 'No events yet'}</span>}
    </button>
  )
  return (
    <div className="relative" ref={ref}>
      <button
        onClick={() => setOpen(!open)}
        className="flex h-11 items-center gap-1.5 rounded-full px-3 text-sm font-medium text-zinc-700 hover:bg-zinc-100"
        aria-expanded={open}
        aria-haspopup="listbox"
      >
        <MapPin className="size-4 text-brand-600" />
        <span className="max-w-36 truncate">{city || province || 'All provinces'}</span>
        <ChevronDown className="size-4 text-zinc-400" />
      </button>
      {open && (
        <div className="absolute right-0 z-50 mt-2 max-h-[70vh] w-72 overflow-y-auto overscroll-contain rounded-2xl bg-white py-2 shadow-xl ring-1 ring-zinc-200" role="listbox" aria-label="Province or city">
          {option('All provinces', !city && !province, () => choose({}), cx('py-2.5 text-sm', !city && !province ? 'font-semibold text-brand-700' : 'font-medium text-ink-900'))}
          {provinces.map((p) => (
            <div key={p.name} className="border-t border-zinc-100 py-1.5" role="group" aria-label={p.name}>
              {option(
                p.name,
                province === p.name && !city,
                () => choose({ province: p.name }),
                cx('py-2 text-sm font-semibold', province === p.name && !city ? 'text-brand-700' : p.event_count > 0 ? 'text-ink-900' : 'text-zinc-500'),
                p.event_count,
              )}
              {p.cities.map((c) => (
                <div key={c.name}>
                  {option(c.name, city === c.name, () => choose({ city: c.name }), cx('py-1.5 pl-8 text-sm', city === c.name ? 'font-semibold text-brand-700' : 'text-zinc-600'), c.event_count)}
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AccountMenu() {
  const { me, logout } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const ref = useDismiss<HTMLDivElement>(open, () => setOpen(false))
  if (!me) return null
  const initials = `${me.user.first_name[0] ?? ''}${me.user.last_name[0] ?? ''}`.toUpperCase()
  const item = (to: string, icon: ReactNode, label: string) => (
    <Link to={to} onClick={() => setOpen(false)} className="flex items-center gap-3 px-4 py-2.5 text-sm text-zinc-700 hover:bg-zinc-50">
      {icon} {label}
    </Link>
  )
  return (
    <div className="relative" ref={ref}>
      <button onClick={() => setOpen(!open)} className="flex items-center gap-2 rounded-full py-1 pl-1 pr-2.5 hover:bg-zinc-100" aria-expanded={open} aria-label="Account menu">
        <span className="flex size-9 items-center justify-center rounded-full bg-ink-900 text-sm font-semibold text-white">{initials}</span>
        <ChevronDown className="size-4 text-zinc-400" />
      </button>
      {open && (
        <div className="absolute right-0 z-50 mt-2 w-64 overflow-hidden rounded-2xl bg-white shadow-xl ring-1 ring-zinc-200">
          <div className="border-b border-zinc-100 px-4 py-3">
            <p className="font-semibold text-ink-900">{me.user.first_name} {me.user.last_name}</p>
            <p className="truncate text-sm text-zinc-500">{me.user.email}</p>
          </div>
          <div className="py-1.5">
            {item('/account/tickets', <Ticket className="size-4 text-zinc-400" />, 'My tickets')}
            {item('/account/orders', <ReceiptText className="size-4 text-zinc-400" />, 'Orders')}
            {item('/account/profile', <Settings className="size-4 text-zinc-400" />, 'Account settings')}
          </div>
          {me.staff_access && (
            <div className="border-t border-zinc-100 py-1.5">
              {item('/admin', <LayoutDashboard className="size-4 text-zinc-400" />, me.organisers.length && !me.all_organisers ? 'Organiser portal' : 'Admin portal')}
            </div>
          )}
          <div className="border-t border-zinc-100 py-1.5">
            <button
              onClick={async () => {
                setOpen(false)
                await logout()
                navigate('/')
              }}
              className="flex w-full items-center gap-3 px-4 py-2.5 text-sm text-zinc-700 hover:bg-zinc-50"
            >
              <LogOut className="size-4 text-zinc-400" /> Sign out
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function CategoryBar() {
  const { data } = useCategories()
  const [params] = useSearchParams()
  const location = useLocation()
  const active = location.pathname === '/events' ? (params.get('category') ?? '') : null
  const when = location.pathname === '/events' ? params.get('when') : null
  const link = (to: string, label: string, isActive: boolean) => (
    <Link
      key={to}
      to={to}
      className={cx(
        'relative whitespace-nowrap py-3 text-sm font-medium transition-colors',
        isActive ? 'text-ink-900 after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:rounded-full after:bg-brand-600' : 'text-zinc-500 hover:text-ink-900',
      )}
    >
      {label}
    </Link>
  )
  return (
    <nav className="border-t border-zinc-100" aria-label="Categories">
      <div className="mx-auto flex max-w-7xl gap-6 overflow-x-auto px-4 [scrollbar-width:none] sm:px-6">
        {link('/events', 'All events', active === '' && !when)}
        {link('/events?when=weekend', 'This weekend', when === 'weekend')}
        {data?.filter((c) => c.event_count > 0).map((c) => link(`/events?category=${c.slug}`, c.name, active === c.slug))}
      </div>
    </nav>
  )
}

function MobileMenu({ onClose }: { onClose: () => void }) {
  const { me, logout } = useAuth()
  const { data: categories } = useCategories()
  const navigate = useNavigate()
  const row = (to: string, label: string) => (
    <Link to={to} onClick={onClose} className="block rounded-xl px-3 py-2.5 text-[15px] text-zinc-800 hover:bg-zinc-50">
      {label}
    </Link>
  )
  return (
    <div className="fixed inset-0 z-50 bg-ink-950/40 md:hidden" onClick={onClose}>
      <div className="ml-auto flex h-full w-[85%] max-w-sm flex-col overflow-y-auto bg-white p-5" onClick={(e) => e.stopPropagation()}>
        <div className="mb-4 flex items-center justify-between">
          <Logo />
          <button onClick={onClose} className="rounded-full p-2 hover:bg-zinc-100" aria-label="Close menu">
            <X className="size-5" />
          </button>
        </div>
        <SearchBox onDone={onClose} />
        <p className="mb-1 mt-6 px-3 text-sm font-semibold text-zinc-400">Browse</p>
        {row('/events', 'All events')}
        {row('/events?when=weekend', 'This weekend')}
        {categories?.filter((c) => c.event_count > 0).map((c) => row(`/events?category=${c.slug}`, c.name))}
        <p className="mb-1 mt-6 px-3 text-sm font-semibold text-zinc-400">{me ? 'Your account' : 'Account'}</p>
        {me ? (
          <>
            {row('/account/tickets', 'My tickets')}
            {row('/account/orders', 'Orders')}
            {row('/account/profile', 'Account settings')}
            {me.staff_access && row('/admin', 'Portal')}
            <button
              onClick={async () => {
                onClose()
                await logout()
                navigate('/')
              }}
              className="block w-full rounded-xl px-3 py-2.5 text-left text-[15px] text-zinc-800 hover:bg-zinc-50"
            >
              Sign out
            </button>
          </>
        ) : (
          <>
            {row('/login', 'Sign in')}
            {row('/register', 'Create an account')}
          </>
        )}
        <p className="mb-1 mt-6 px-3 text-sm font-semibold text-zinc-400">More</p>
        {row('/sell', 'Sell tickets')}
        {row('/help', 'Help centre')}
        <InstallApp onDone={onClose} className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-[15px] font-medium text-brand-700 hover:bg-zinc-50" />
      </div>
    </div>
  )
}

function Footer() {
  const { data: categories } = useCategories()
  const { me } = useAuth()
  const col = (title: string, links: [string, string][]) => (
    <div>
      <h3 className="font-sans text-sm font-semibold text-white">{title}</h3>
      <ul className="mt-4 space-y-2.5">
        {links.map(([to, label]) => (
          <li key={to}>
            <Link to={to} className="text-sm text-ink-200 hover:text-white">{label}</Link>
          </li>
        ))}
      </ul>
    </div>
  )
  return (
    <footer className="mt-16 bg-ink-900 text-ink-200">
      <div className="mx-auto grid max-w-7xl gap-10 px-4 py-14 sm:px-6 md:grid-cols-[1.4fr_1fr_1fr_1fr]">
        <div>
          <Logo light />
          <p className="mt-4 max-w-xs text-sm leading-relaxed">
            Tickets for concerts, comedy, festivals and conferences across South Africa, with secure checkout and tickets that live on your phone.
          </p>
          <div className="mt-6 flex flex-wrap gap-2 text-xs">
            {['Visa', 'Mastercard', 'Instant EFT', 'Bank transfer'].map((m) => (
              <span key={m} className="rounded-md border border-ink-600 px-2 py-1 text-ink-200">{m}</span>
            ))}
          </div>
          <InstallApp className="mt-6 inline-flex items-center gap-2 rounded-full bg-white px-4 py-2 text-sm font-semibold text-ink-900 hover:bg-ink-200" />
        </div>
        {col('Discover', [
          ['/events', 'All events'],
          ['/events?when=weekend', 'This weekend'],
          ...((categories ?? []).filter((c) => c.event_count > 0).slice(0, 4).map((c) => [`/events?category=${c.slug}`, c.name]) as [string, string][]),
        ])}
        {col('Organisers', [
          ['/sell', 'Sell tickets'],
          ['/organisers/apply', 'Apply as an organiser'],
          ...(me?.staff_access ? ([['/admin', 'Organiser portal'], ['/scan', 'Door scanner']] as [string, string][]) : []),
        ])}
        {col('Help', [
          ['/help', 'Help centre'],
          ['/help#refunds', 'Refunds'],
          ['/terms', 'Terms of sale'],
          ['/account/tickets', 'Find my tickets'],
        ])}
      </div>
      <div className="border-t border-ink-700">
        <div className="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-3 px-4 py-6 text-sm sm:px-6">
          <span>© {new Date().getFullYear()} {appName}. All prices are in South African rand.</span>
          <Link to="/help#contact" className="hover:text-white">Contact us</Link>
        </div>
      </div>
    </footer>
  )
}

export function PublicLayout() {
  const { me, loading } = useAuth()
  const location = useLocation()
  const [menuOpen, setMenuOpen] = useState(false)
  const [searchOpen, setSearchOpen] = useState(false)

  useEffect(() => {
    setMenuOpen(false)
    setSearchOpen(false)
  }, [location.pathname, location.search])

  // Start each new page at the top (filter changes on the same page keep their position).
  useEffect(() => {
    window.scrollTo(0, 0)
  }, [location.pathname])

  return (
    <div className="flex min-h-screen flex-col">
      <header className="sticky top-0 z-40 bg-white/95 backdrop-blur supports-[backdrop-filter]:bg-white/85">
        <div className="mx-auto flex h-[4.5rem] max-w-7xl items-center gap-4 px-4 sm:px-6">
          <Logo />
          <SearchBox className="mx-auto hidden w-full min-w-0 max-w-xl md:block" />
          <div className="ml-auto hidden shrink-0 items-center gap-1 whitespace-nowrap md:flex">
            <LocationPicker />
            <NavLink to="/sell" className="hidden rounded-full px-3 py-2.5 text-sm font-medium text-zinc-700 hover:bg-zinc-100 lg:block">Sell tickets</NavLink>
            <NavLink to="/help" className="rounded-full p-2.5 text-zinc-500 hover:bg-zinc-100" aria-label="Help centre">
              <CircleHelp className="size-5" />
            </NavLink>
            {me?.staff_access && (
              <Link to="/scan" className="rounded-full p-2.5 text-zinc-500 hover:bg-zinc-100" aria-label="Door scanner">
                <QrCode className="size-5" />
              </Link>
            )}
            {loading ? (
              <span className="ml-2 size-9 rounded-full bg-zinc-100" />
            ) : me ? (
              <div className="ml-1 flex items-center gap-1">
                <Link to="/account/tickets" className="rounded-full px-3 py-2.5 text-sm font-medium text-zinc-700 hover:bg-zinc-100">My tickets</Link>
                <AccountMenu />
              </div>
            ) : (
              <div className="ml-2 flex items-center gap-2">
                <Link to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} className="rounded-full px-4 py-2.5 text-sm font-semibold text-ink-900 hover:bg-zinc-100">Sign in</Link>
                <Link to="/register" className="rounded-full bg-ink-900 px-4 py-2.5 text-sm font-semibold text-white hover:bg-ink-800">Sign up</Link>
              </div>
            )}
          </div>
          <div className="ml-auto flex items-center gap-1 md:hidden">
            <button onClick={() => setSearchOpen(!searchOpen)} className="rounded-full p-2.5 text-zinc-700 hover:bg-zinc-100" aria-label="Search">
              <Search className="size-5" />
            </button>
            <button onClick={() => setMenuOpen(true)} className="rounded-full p-2.5 text-zinc-700 hover:bg-zinc-100" aria-label="Open menu">
              <Menu className="size-5" />
            </button>
          </div>
        </div>
        {searchOpen && (
          <div className="px-4 pb-3 md:hidden">
            <SearchBox autoFocus />
          </div>
        )}
        <CategoryBar />
      </header>
      {menuOpen && <MobileMenu onClose={() => setMenuOpen(false)} />}
      <main className="flex-1">
        <Outlet />
      </main>
      <Footer />
    </div>
  )
}
