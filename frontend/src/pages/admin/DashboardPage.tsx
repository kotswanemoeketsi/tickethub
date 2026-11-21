import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { Banknote, CalendarCheck, ClipboardCheck, Ticket } from 'lucide-react'
import { api, errorMessage } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDate, formatDateTime, formatEventDate, formatMoney, percent } from '../../lib/format'
import { useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { DashboardSummary } from '../../lib/types'
import { Alert, Card, CardHeader, EmptyState, Loading, PageHeader, ProgressBar, Stat } from '../../components/ui'
import { EventPicker } from '../../components/EventPicker'

export default function DashboardPage() {
  const { me, can } = useAuth()
  const [eventId, setEventId] = useQueryParam('event')
  const { data, isLoading, error } = useQuery({
    queryKey: ['dashboard', eventId],
    queryFn: () => api<DashboardSummary>('/admin/dashboard', { query: { event_id: eventId } }),
    refetchInterval: 60_000,
    placeholderData: (previous) => previous, // keep the last figures on screen while switching events
  })
  const header = (
    <PageHeader
      title={`Good to see you, ${me?.user.first_name}`}
      description={data?.event ? `Showing ${data.event.title}, ${formatEventDate(data.event.starts_at, data.event.ends_at, data.event.timezone)}.` : 'Here is how your events are doing.'}
      actions={can(P.EventsView) && <EventPicker label="Show" className="w-80" value={eventId} onChange={setEventId} />}
    />
  )
  if (error) {
    return (
      <>
        {header}
        <Alert tone="error">{errorMessage(error)} <button className="font-semibold underline" onClick={() => setEventId('')}>Show all events</button></Alert>
      </>
    )
  }
  if (isLoading || !data) return <Loading />
  const money = (c: number) => formatMoney(c, data.currency)
  const showFinance = can(P.ReportsFinancial) || can(P.PaymentsView)

  const todo = [
    can(P.EventsReview) && data.pending_review > 0 && { to: '/admin/approvals', text: `${data.pending_review} event${data.pending_review === 1 ? '' : 's'} awaiting approval` },
    can(P.PaymentsConfirm) && data.pending_eft_payments > 0 && { to: '/admin/payments?status=pending&method=eft', text: `${data.pending_eft_payments} EFT payment${data.pending_eft_payments === 1 ? '' : 's'} to confirm` },
    can(P.RefundsManage) && data.pending_refunds > 0 && { to: '/admin/refunds?status=requested', text: `${data.pending_refunds} refund request${data.pending_refunds === 1 ? '' : 's'} to review` },
  ].filter(Boolean) as { to: string; text: string }[]

  return (
    <>
      {header}

      {todo.length > 0 && (
        <div className="mb-6">
          <Alert tone="warning" title="Needs your attention">
            <ul className="mt-1 space-y-0.5">
              {todo.map((t) => <li key={t.to}><Link to={t.to} className="underline">{t.text}</Link></li>)}
            </ul>
          </Alert>
        </div>
      )}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {showFinance && <Stat label="Net revenue" value={money(data.net_revenue_cents)} hint={`${money(data.revenue_last_30_days_cents)} in the last 30 days`} icon={<Banknote className="size-5" />} />}
        <Stat label="Tickets sold" value={data.tickets_sold.toLocaleString()} hint={`${data.orders_paid.toLocaleString()} paid orders`} icon={<Ticket className="size-5" />} />
        {data.event ? (
          <Stat label="Capacity sold" value={percent(data.inventory.sold, data.inventory.capacity)} hint={`${data.inventory.sold.toLocaleString()} of ${data.inventory.capacity.toLocaleString()} tickets`} icon={<CalendarCheck className="size-5" />} />
        ) : (
          <Stat label="Upcoming events" value={data.upcoming_events} hint={`${data.live_events} on now, ${data.draft_events} ${data.draft_events === 1 ? 'draft' : 'drafts'}`} icon={<CalendarCheck className="size-5" />} />
        )}
        <Stat
          label="Attendance"
          value={percent(data.attendance.checked_in, data.attendance.tickets_issued)}
          hint={`${data.attendance.checked_in.toLocaleString()} of ${data.attendance.tickets_issued.toLocaleString()} tickets checked in`}
          icon={<ClipboardCheck className="size-5" />}
        />
      </div>

      <div className="mt-6 grid items-start gap-6 xl:grid-cols-3">
        <Card className="xl:col-span-2">
          <CardHeader title="Sales over the last 30 days" />
          <div className="h-72 p-4">
            {data.sales_trend.length === 0 ? (
              <EmptyState title="No sales yet" />
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={data.sales_trend} margin={{ left: 8, right: 8, top: 8 }}>
                  <defs>
                    <linearGradient id="rev" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="0%" stopColor="#4f46e5" stopOpacity={0.3} />
                      <stop offset="100%" stopColor="#4f46e5" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="#f4f4f5" vertical={false} />
                  <XAxis dataKey="date" tickFormatter={(d: string) => formatDate(d).replace(/ \d{4}$/, '')} tick={{ fontSize: 12, fill: '#71717a' }} tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis
                    yAxisId="v"
                    tickFormatter={(c: number) => (showFinance ? `R${Math.round(c / 100).toLocaleString()}` : String(c))}
                    tick={{ fontSize: 12, fill: '#71717a' }}
                    tickLine={false}
                    axisLine={false}
                    width={72}
                  />
                  <Tooltip
                    formatter={(value, name) => (name === 'revenue_cents' ? [money(Number(value)), 'Revenue'] : [value, 'Tickets'])}
                    labelFormatter={(d) => formatDate(String(d))}
                  />
                  <Area yAxisId="v" type="monotone" dataKey={showFinance ? 'revenue_cents' : 'tickets'} stroke="#4f46e5" strokeWidth={2} fill="url(#rev)" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>
        </Card>

        <Card>
          <CardHeader title="Inventory" description={data.event ? 'For this event' : 'Across upcoming events'} />
          <div className="space-y-4 p-5 text-sm">
            <ProgressBar value={data.inventory.sold} max={data.inventory.capacity} />
            <div className="grid grid-cols-3 gap-2 text-center">
              <div><p className="text-lg font-semibold">{data.inventory.sold.toLocaleString()}</p><p className="text-xs text-zinc-500">Sold</p></div>
              <div><p className="text-lg font-semibold">{data.inventory.reserved.toLocaleString()}</p><p className="text-xs text-zinc-500">In checkout</p></div>
              <div><p className="text-lg font-semibold">{data.inventory.available.toLocaleString()}</p><p className="text-xs text-zinc-500">Available</p></div>
            </div>
            {!data.event && <div className="border-t border-zinc-100 pt-4">
              <p className="mb-2 font-medium">Next up</p>
              {data.upcoming_list.length === 0 ? (
                <p className="text-zinc-500">No upcoming events.</p>
              ) : (
                <ul className="space-y-3">
                  {data.upcoming_list.map((e) => (
                    <li key={e.id}>
                      <Link to={`/admin/events/${e.id}`} className="font-medium text-zinc-900 hover:text-brand-700">{e.title}</Link>
                      <p className="text-xs text-zinc-500">{formatDateTime(e.starts_at, e.timezone)}, {e.sold} of {e.capacity} sold</p>
                      <ProgressBar value={e.sold} max={e.capacity} className="mt-1 h-1.5" />
                    </li>
                  ))}
                </ul>
              )}
            </div>}
            {data.event && (
              <Link to={`/admin/events/${data.event.id}`} className="block border-t border-zinc-100 pt-4 font-medium text-brand-700 hover:text-brand-800">
                Open the event
              </Link>
            )}
          </div>
        </Card>
      </div>

      {showFinance && (
        <Card className="mt-6">
          <CardHeader title="Recent transactions" />
          {data.recent_transactions.length === 0 ? (
            <EmptyState title="No transactions yet" />
          ) : (
            <ul className="divide-y divide-zinc-100">
              {data.recent_transactions.map((t) => (
                <li key={t.order_id} className="flex flex-wrap items-center justify-between gap-2 px-5 py-3 text-sm">
                  <div>
                    <Link to={`/admin/orders/${t.order_id}`} className="font-mono font-medium text-zinc-900 hover:text-brand-700">{t.order_reference}</Link>
                    <p className="text-zinc-500">{t.customer_name}, {t.event_title}</p>
                  </div>
                  <div className="flex items-center gap-4">
                    <span className="text-xs text-zinc-400">{t.method === 'eft' ? 'EFT' : 'Online'}</span>
                    <span className="text-zinc-500">{formatDateTime(t.paid_at)}</span>
                    <span className="font-semibold tabular-nums">{money(t.amount_cents)}</span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
    </>
  )
}
