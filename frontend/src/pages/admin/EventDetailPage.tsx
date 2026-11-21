import { useState, type ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, CheckCircle2, ExternalLink, Pencil, Send, Trash2, Undo2, XCircle } from 'lucide-react'
import { api, errorMessage } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatEventDate, formatMoney, percent } from '../../lib/format'
import { useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { AdminEvent } from '../../lib/types'
import { Alert, Button, ButtonLink, Card, CardHeader, DescriptionList, EmptyState, Loading, Modal, PageHeader, ProgressBar, Stat, StatusBadge, Tabs, Textarea } from '../../components/ui'
import { useToast } from '../../components/toast'
import { EventPreCheck } from '../../components/EventPreCheck'
import { useFeatures } from '../../lib/ai'
import { TicketTypesTab } from './event/TicketTypesTab'
import { PromotionsTab } from './event/PromotionsTab'
import { AttendeesTab, CheckinTab, EventOrdersTab } from './event/PeopleTabs'

type Action = 'submit' | 'withdraw' | 'approve' | 'reject' | 'cancel'

const actionCopy: Record<Action, { title: string; description: string; confirm: string; field?: 'notes' | 'reason'; required?: boolean; danger?: boolean }> = {
  submit: { title: 'Submit for approval', description: 'The platform team reviews the event before it goes on sale.', confirm: 'Submit' },
  withdraw: { title: 'Withdraw submission', description: 'The event returns to draft so you can keep editing.', confirm: 'Withdraw' },
  approve: { title: 'Approve and publish', description: 'The event goes live on the marketplace immediately.', confirm: 'Approve', field: 'notes' },
  reject: { title: 'Reject event', description: 'The organiser sees your notes and can resubmit.', confirm: 'Reject', field: 'notes', required: true, danger: true },
  cancel: {
    title: 'Cancel event',
    description: 'Sales stop, every ticket is voided and ticket holders are emailed. A full refund is approved for every paid order and queued on the Refunds page for finance to pay out. This cannot be undone.',
    confirm: 'Cancel event',
    field: 'reason',
    required: true,
    danger: true,
  },
}

type TabKey = 'overview' | 'tickets' | 'promotions' | 'attendees' | 'orders' | 'checkin'

export default function EventDetailPage() {
  const features = useFeatures()
  const { eventId = '' } = useParams()
  const { canFor } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [tab, setTab] = useQueryParam('tab', 'overview')
  const [action, setAction] = useState<Action | null>(null)
  const [note, setNote] = useState('')

  const key = ['admin', 'event', eventId]
  const event = useQuery({ queryKey: key, queryFn: () => api<AdminEvent>(`/admin/events/${eventId}`) })
  const lifecycle = useMutation({
    mutationFn: (a: Action) => api<AdminEvent>(`/admin/events/${eventId}/${a}`, { method: 'POST', body: a === 'cancel' ? { reason: note } : { notes: note } }),
    onSuccess: (_, a) => {
      queryClient.invalidateQueries({ queryKey: ['admin'] })
      toast.success({ submit: 'Submitted for approval.', withdraw: 'Moved back to draft.', approve: 'Event approved and published.', reject: 'Event rejected.', cancel: 'Event cancelled. Full refunds are waiting on the Refunds page.' }[a])
      setAction(null)
      setNote('')
    },
  })
  const remove = useMutation({
    mutationFn: () => api(`/admin/events/${eventId}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'events'] })
      toast.success('Draft deleted.')
      navigate('/admin/events')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  if (event.isLoading) return <Loading />
  if (!event.data) return <EmptyState title="Event not found" />
  const e = event.data
  const can = (perm: string) => canFor(e.organiser_id, perm)
  const editable = e.status !== 'cancelled' && e.phase !== 'completed'
  const money = (c: number) => formatMoney(c, e.currency)

  const actions: Action[] = []
  if (can(P.EventsUpdate) && (e.status === 'draft' || e.status === 'rejected')) actions.push('submit')
  if (can(P.EventsUpdate) && e.status === 'pending_review') actions.push('withdraw')
  if (can(P.EventsReview) && e.status === 'pending_review') actions.push('approve', 'reject')
  if (can(P.EventsCancel) && e.status === 'published' && e.phase !== 'completed') actions.push('cancel')

  const icons: Record<Action, ReactNode> = {
    submit: <Send className="size-4" />,
    withdraw: <Undo2 className="size-4" />,
    approve: <CheckCircle2 className="size-4" />,
    reject: <XCircle className="size-4" />,
    cancel: <Ban className="size-4" />,
  }

  const tabs: { value: TabKey; label: string }[] = [
    { value: 'overview', label: 'Overview' },
    { value: 'tickets', label: 'Ticket types' },
    ...(can(P.PromotionsManage) ? [{ value: 'promotions' as const, label: 'Promo codes' }] : []),
    ...(can(P.AttendeesView) ? [{ value: 'attendees' as const, label: 'Attendees' }] : []),
    ...(can(P.OrdersView) ? [{ value: 'orders' as const, label: 'Orders' }] : []),
    ...(can(P.AttendeesView) || can(P.CheckinScan) ? [{ value: 'checkin' as const, label: 'Check-in' }] : []),
  ]
  const active = (tabs.some((t) => t.value === tab) ? tab : 'overview') as TabKey
  const copy = action ? actionCopy[action] : null

  return (
    <>
      <PageHeader
        back={{ to: '/admin/events', label: 'Events' }}
        title={<span className="flex flex-wrap items-center gap-3">{e.title} <StatusBadge status={e.status} />{e.status === 'published' && <StatusBadge status={e.phase} />}</span>}
        description={`${e.organiser_name}, ${formatEventDate(e.starts_at, e.ends_at, e.timezone)}`}
        actions={
          <>
            {e.status === 'published' && <a href={`/events/${e.slug}`} target="_blank" rel="noreferrer" className="inline-flex h-10 items-center gap-2 rounded-lg px-4 text-sm font-medium text-zinc-700 hover:bg-zinc-100"><ExternalLink className="size-4" /> View page</a>}
            {editable && can(P.EventsUpdate) && <ButtonLink to={`/admin/events/${e.id}/edit`} variant="secondary"><Pencil className="size-4" /> Edit</ButtonLink>}
            {actions.map((a) => (
              <Button key={a} variant={actionCopy[a].danger ? 'danger' : a === 'approve' ? 'success' : 'primary'} icon={icons[a]} onClick={() => setAction(a)}>
                {actionCopy[a].confirm}
              </Button>
            ))}
            {e.status === 'draft' && can(P.EventsDelete) && (
              <Button variant="ghost" icon={<Trash2 className="size-4" />} loading={remove.isPending} onClick={() => window.confirm('Delete this draft event?') && remove.mutate()} aria-label="Delete draft" />
            )}
          </>
        }
      />

      {e.status === 'rejected' && e.review_notes && <div className="mb-4"><Alert tone="error" title="Changes requested">{e.review_notes}</Alert></div>}
      {e.status === 'cancelled' && <div className="mb-4"><Alert tone="error" title={`Cancelled ${formatDateTime(e.cancelled_at)}`}>{e.cancellation_reason}</Alert></div>}

      <Tabs tabs={tabs} value={active} onChange={(v) => setTab(v === 'overview' ? '' : v)} />

      {active === 'overview' && (
        <div className="space-y-6">
          {features.ai_event_review && can(P.EventsReview) && e.status === 'pending_review' && <EventPreCheck eventId={e.id} />}
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <Stat label="Tickets sold" value={`${e.stats.tickets_sold} / ${e.capacity}`} hint={`${e.stats.tickets_reserved} in checkout, ${e.stats.tickets_available} available`} />
            {can(P.ReportsFinancial) || can(P.OrdersView) ? <Stat label="Net revenue" value={money(e.stats.net_revenue_cents)} hint={`${e.stats.orders_paid} paid orders`} /> : null}
            <Stat label="Checked in" value={e.stats.checked_in} hint={`${percent(e.stats.checked_in, e.stats.tickets_sold)} of tickets sold`} />
            <Stat label="Ticket types" value={e.stats.ticket_types} />
          </div>
          <div className="grid gap-6 xl:grid-cols-3">
            <Card className="xl:col-span-2">
              <CardHeader title="Event details" />
              <div className="px-5 pb-3">
                <DescriptionList
                  items={[
                    { label: 'Category', value: e.category_name || '—' },
                    { label: 'Venue', value: e.venue ? `${e.venue.name}, ${e.venue.address_line}, ${e.venue.city}` : '—' },
                    { label: 'Time zone', value: e.timezone },
                    { label: 'Re-entry', value: e.allow_reentry ? 'Allowed' : 'Not allowed' },
                    { label: 'Refunds', value: e.refunds_allowed ? `Up to ${e.refund_cutoff_hours} hours before the event` : 'Not offered' },
                    { label: 'Summary', value: e.summary || '—' },
                    { label: 'Created', value: formatDateTime(e.created_at) },
                    { label: 'Submitted', value: formatDateTime(e.submitted_at) },
                    { label: 'Published', value: formatDateTime(e.published_at) },
                  ]}
                />
              </div>
            </Card>
            <Card>
              <CardHeader title="Capacity" />
              <div className="space-y-3 p-5 text-sm">
                <ProgressBar value={e.stats.tickets_sold} max={e.capacity} />
                <p className="text-zinc-500">{percent(e.stats.tickets_sold, e.capacity)} sold</p>
                {e.image_url && <img src={e.image_url} alt="" className="mt-4 aspect-video w-full rounded-lg object-cover" />}
              </div>
            </Card>
          </div>
        </div>
      )}
      {active === 'tickets' && <TicketTypesTab event={e} canManage={editable && can(P.TicketTypesManage)} />}
      {active === 'promotions' && <PromotionsTab event={e} />}
      {active === 'attendees' && <AttendeesTab event={e} canComp={can(P.TicketsComplimentary)} />}
      {active === 'orders' && <EventOrdersTab event={e} />}
      {active === 'checkin' && <CheckinTab event={e} />}

      <Modal
        open={!!action}
        onClose={() => { setAction(null); lifecycle.reset() }}
        title={copy?.title}
        description={copy?.description}
        footer={
          <>
            <Button variant="secondary" onClick={() => setAction(null)}>Back</Button>
            <Button
              variant={copy?.danger ? 'danger' : 'primary'}
              loading={lifecycle.isPending}
              disabled={!!copy?.required && !note.trim()}
              onClick={() => action && lifecycle.mutate(action)}
            >
              {copy?.confirm}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {lifecycle.error && <Alert tone="error">{errorMessage(lifecycle.error)}</Alert>}
          {copy?.field && (
            <Textarea
              label={copy.field === 'reason' ? 'Reason (sent to ticket holders)' : copy.required ? 'What needs to change?' : 'Notes for the organiser (optional)'}
              value={note}
              onChange={(ev) => setNote(ev.target.value)}
              rows={4}
            />
          )}
          {!copy?.field && <p className="text-sm text-zinc-600">Are you sure?</p>}
        </div>
      </Modal>
    </>
  )
}
