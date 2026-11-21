import { useState, type ReactNode } from 'react'
import { Link, NavLink, Outlet, useNavigate } from 'react-router'
import {
  BadgeCheck, BarChart3, Building2, CalendarDays, CreditCard, LayoutDashboard, LogOut, Menu, QrCode,
  ReceiptText, RotateCcw, ScrollText, Shapes, ShieldCheck, Store, Users, X,
} from 'lucide-react'
import { useAuth } from '../lib/auth'
import { P } from '../lib/permissions'
import { cx } from '../components/ui'
import { Logo } from './PublicLayout'

interface NavItem {
  to: string
  label: string
  icon: ReactNode
  visible: boolean
  end?: boolean
}

export function AdminLayout() {
  const { me, can, canPlatform, logout } = useAuth()
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  if (!me) return null

  const teamOrganisers = me.organisers.filter((o) => o.permissions.includes(P.TeamManage))
  const sections: { title: string; items: NavItem[] }[] = [
    {
      title: 'Operations',
      items: [
        { to: '/admin', label: 'Dashboard', icon: <LayoutDashboard className="size-4" />, visible: can(P.DashboardView), end: true },
        { to: '/admin/events', label: 'Events', icon: <CalendarDays className="size-4" />, visible: can(P.EventsView) },
        { to: '/admin/approvals', label: 'Approvals', icon: <BadgeCheck className="size-4" />, visible: canPlatform(P.EventsReview) },
        { to: '/admin/orders', label: 'Orders', icon: <ReceiptText className="size-4" />, visible: can(P.OrdersView) },
        { to: '/admin/payments', label: 'Payments', icon: <CreditCard className="size-4" />, visible: can(P.PaymentsView) },
        { to: '/admin/refunds', label: 'Refunds', icon: <RotateCcw className="size-4" />, visible: can(P.RefundsView) },
        { to: '/admin/reports', label: 'Reports', icon: <BarChart3 className="size-4" />, visible: can(P.ReportsView) || can(P.ReportsFinancial) },
        { to: '/scan', label: 'Scan tickets', icon: <QrCode className="size-4" />, visible: can(P.CheckinScan) },
      ],
    },
    {
      title: 'Administration',
      items: [
        { to: '/admin/organisers', label: 'Organisers', icon: <Building2 className="size-4" />, visible: canPlatform(P.OrganisersView) },
        ...teamOrganisers.map((o) => ({ to: `/admin/organisers/${o.id}`, label: o.name, icon: <Store className="size-4" />, visible: !canPlatform(P.OrganisersView) })),
        { to: '/admin/users', label: 'Users', icon: <Users className="size-4" />, visible: canPlatform(P.UsersView) },
        { to: '/admin/roles', label: 'Roles & permissions', icon: <ShieldCheck className="size-4" />, visible: canPlatform(P.RolesManage) },
        { to: '/admin/catalogue', label: 'Categories & venues', icon: <Shapes className="size-4" />, visible: canPlatform(P.CategoriesManage) || can(P.VenuesManage) },
        { to: '/admin/audit', label: 'Audit log', icon: <ScrollText className="size-4" />, visible: can(P.AuditView) },
      ],
    },
  ]

  const nav = (
    <nav className="flex flex-col gap-6 px-3 py-4">
      {sections.map((section) => {
        const items = section.items.filter((i) => i.visible)
        if (items.length === 0) return null
        return (
          <div key={section.title}>
            <p className="px-3 pb-2 text-xs font-medium text-ink-300">{section.title}</p>
            {items.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                onClick={() => setOpen(false)}
                className={({ isActive }) =>
                  cx('flex items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition-colors', isActive ? 'bg-white/10 text-white [&_svg]:text-marigold-400' : 'text-ink-200 hover:bg-white/5 hover:text-white')
                }
              >
                {item.icon}
                <span className="truncate">{item.label}</span>
              </NavLink>
            ))}
          </div>
        )
      })}
    </nav>
  )

  const scope = me.all_organisers ? 'All organisers' : me.organisers.map((o) => o.name).join(', ') || 'No organiser'

  return (
    <div className="min-h-screen lg:grid lg:grid-cols-[260px_1fr] lg:bg-[linear-gradient(to_right,var(--color-ink-900)_260px,transparent_260px)]">
      <aside className={cx('fixed inset-y-0 left-0 z-40 flex w-72 flex-col bg-ink-900 transition-transform lg:sticky lg:top-0 lg:h-screen lg:w-auto lg:translate-x-0', open ? 'translate-x-0' : '-translate-x-full')}>
        <div className="flex h-[4.5rem] items-center justify-between px-5">
          <Logo light />
          <button className="lg:hidden" onClick={() => setOpen(false)} aria-label="Close menu">
            <X className="size-5 text-ink-200" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto">{nav}</div>
        <div className="border-t border-white/10 p-4">
          <p className="truncate text-sm font-semibold text-white">{me.user.first_name} {me.user.last_name}</p>
          <p className="truncate text-xs text-ink-300" title={scope}>{scope}</p>
          <div className="mt-3 flex gap-3 text-sm">
            <Link to="/" className="text-ink-200 hover:text-white">Marketplace</Link>
            <button onClick={async () => { await logout(); navigate('/login') }} className="flex items-center gap-1 text-ink-200 hover:text-white">
              <LogOut className="size-3.5" /> Sign out
            </button>
          </div>
        </div>
      </aside>
      {open && <div className="fixed inset-0 z-30 bg-ink-950/40 lg:hidden" onClick={() => setOpen(false)} />}
      <div className="min-w-0">
        <header className="sticky top-0 z-20 flex h-16 items-center gap-3 border-b border-zinc-200 bg-white/90 px-4 backdrop-blur lg:hidden">
          <button onClick={() => setOpen(true)} aria-label="Open menu">
            <Menu className="size-5 text-zinc-600" />
          </button>
          <Logo />
        </header>
        <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 lg:px-10 lg:py-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
