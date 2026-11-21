import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Download, MapPin, QrCode, Ticket as TicketIcon, WifiOff } from 'lucide-react'
import { api, download, errorMessage, isNetworkError } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatEventDate, fullName } from '../../lib/format'
import type { Ticket } from '../../lib/types'
import { readWallet, saveWallet } from '../../lib/wallet'
import { Alert, Badge, ButtonLink, EmptyState, Loading, Modal, StatusBadge, Tabs } from '../../components/ui'
import { QRCode } from '../../components/QRCode'
import { useToast } from '../../components/toast'
import { DateStub, EventImage } from '../../components/events'

/** One ticket, drawn as a ticket: details on the left, the QR stub on the right. */
function WalletTicket({ ticket, offline, onShow, onDownload }: { ticket: Ticket; offline: boolean; onShow: () => void; onDownload: () => void }) {
  const usable = ticket.status === 'valid'
  return (
    <div className="flex overflow-hidden rounded-2xl bg-white shadow-[0_1px_2px_rgba(21,18,58,0.06),0_8px_24px_-14px_rgba(21,18,58,0.25)]">
      <div className="min-w-0 flex-1 p-5">
        <div className="flex flex-wrap items-center gap-2">
          <p className="font-semibold text-ink-900">{ticket.ticket_type_name}</p>
          {ticket.is_complimentary && <Badge tone="purple">Complimentary</Badge>}
          {!usable ? <StatusBadge status={ticket.status} /> : ticket.checkin_state !== 'not_checked_in' && <StatusBadge status={ticket.checkin_state} />}
        </div>
        <p className="mt-1 text-sm text-zinc-500">{fullName(ticket.holder_first_name, ticket.holder_last_name)}</p>
        <p className="mt-0.5 font-mono text-sm tracking-wider text-zinc-400">{ticket.display_code}</p>
        {!offline && <div className="mt-4 flex flex-wrap gap-2">
          <button onClick={onDownload} className="flex items-center gap-1.5 rounded-full px-3 py-1.5 text-sm font-medium text-zinc-700 ring-1 ring-zinc-200 hover:bg-zinc-50">
            <Download className="size-4" /> PDF
          </button>
          <Link to={`/account/orders/${ticket.order_id}`} className="rounded-full px-3 py-1.5 text-sm font-medium text-zinc-700 ring-1 ring-zinc-200 hover:bg-zinc-50">
            Order {ticket.order_reference}
          </Link>
        </div>}
      </div>
      <button
        onClick={onShow}
        disabled={!usable || !ticket.qr_payload}
        className="stub-line flex w-32 shrink-0 flex-col items-center justify-center gap-2 bg-zinc-50 text-brand-700 hover:bg-brand-50 disabled:text-zinc-300 disabled:hover:bg-zinc-50 sm:w-36"
        style={{ ['--stub-bg' as string]: 'var(--color-canvas)' }}
        aria-label={`Show QR code for ${ticket.ticket_type_name} ${ticket.display_code}`}
      >
        <QrCode className="size-10" />
        <span className="text-sm font-semibold">{usable ? 'Show QR' : 'Not valid'}</span>
      </button>
    </div>
  )
}

/**
 * The user's tickets. Upcoming tickets are also saved on the device, so without a
 * connection (offline set, or the request failing) the saved copy is shown instead.
 */
export function MyTicketsPage({ offline = false }: { offline?: boolean }) {
  const { me } = useAuth()
  const [when, setWhen] = useState<'upcoming' | 'past'>('upcoming')
  const [open, setOpen] = useState<Ticket | null>(null)
  const toast = useToast()
  const tickets = useQuery({
    queryKey: ['my-tickets', when],
    queryFn: () => api<Ticket[]>('/tickets', { query: { when: when === 'past' ? 'past' : '' } }),
    enabled: !offline,
  })

  useEffect(() => {
    if (me && when === 'upcoming' && tickets.data) saveWallet(me.user.id, tickets.data)
  }, [me, when, tickets.data])

  const unreachable = offline || (tickets.isError && isNetworkError(tickets.error))
  const wallet = useMemo(() => (unreachable ? readWallet(me?.user.id) : null), [unreachable, me])
  const list = useMemo(() => (unreachable ? (wallet?.tickets ?? []) : (tickets.data ?? [])), [unreachable, wallet, tickets.data])

  // Group by event, keeping the API's chronological order.
  const groups = useMemo(() => {
    const map = new Map<number, Ticket[]>()
    for (const t of list) {
      const key = t.event?.id ?? 0
      map.set(key, [...(map.get(key) ?? []), t])
    }
    return [...map.values()]
  }, [list])

  const downloadPdf = (t: Ticket) =>
    download(`/orders/${t.order_id}/tickets.pdf`, { ticket_id: t.id }, `ticket-${t.code}.pdf`).catch((e) => toast.error(errorMessage(e)))

  return (
    <div className="mx-auto max-w-5xl px-4 py-8 sm:px-6 sm:py-10">
      <h1 className="text-3xl font-bold text-ink-900">My tickets</h1>
      {unreachable ? (
        <div className="mb-6 mt-6">
          <Alert tone="info" title="You're offline">
            {wallet
              ? `Showing the tickets saved on this device on ${formatDateTime(wallet.savedAt)}. They scan at the door as normal.`
              : 'Your tickets will appear once you are back online.'}
          </Alert>
        </div>
      ) : (
        <div className="mt-6">
          <Tabs value={when} onChange={setWhen} tabs={[{ value: 'upcoming', label: 'Upcoming' }, { value: 'past', label: 'Past events' }]} />
        </div>
      )}

      {tickets.isLoading && !unreachable ? (
        <Loading />
      ) : unreachable && !wallet ? (
        <div className="rounded-3xl bg-white ring-1 ring-zinc-200">
          <EmptyState
            icon={<WifiOff className="size-6" />}
            title="No tickets saved on this device"
            description="Open My tickets once while you are connected, and they will be here next time, even without signal."
          />
        </div>
      ) : tickets.isError && !unreachable ? (
        <Alert tone="error">{errorMessage(tickets.error)}</Alert>
      ) : groups.length === 0 ? (
        <div className="rounded-3xl bg-white ring-1 ring-zinc-200">
          <EmptyState
            icon={<TicketIcon className="size-6" />}
            title={when === 'upcoming' ? 'No upcoming tickets yet' : 'No past events'}
            description="Tickets you buy appear here, ready to show at the door."
            action={when === 'upcoming' && <ButtonLink to="/events">Find something to do</ButtonLink>}
          />
        </div>
      ) : (
        <div className="space-y-10">
          {groups.map((list) => {
            const ev = list[0].event
            return (
              <section key={ev?.id ?? 0} className="grid gap-5 md:grid-cols-[260px_1fr]">
                {ev && (
                  <Link to={`/events/${ev.slug}`} className="group block">
                    <div className="aspect-[16/10] overflow-hidden rounded-2xl bg-ink-900">
                      <EventImage event={{ image_url: ev.image_url, title: ev.title, category_name: '' }} className="transition-transform duration-500 group-hover:scale-[1.04]" />
                    </div>
                    <div className="mt-3 flex gap-3">
                      <DateStub iso={ev.starts_at} timeZone={ev.timezone} className="w-12" />
                      <div className="min-w-0">
                        <h2 className="font-display text-lg font-semibold leading-snug text-ink-900 group-hover:text-brand-700">{ev.title}</h2>
                        <p className="mt-0.5 flex items-center gap-1 text-sm text-zinc-500"><MapPin className="size-3.5" /> {ev.venue_name}</p>
                      </div>
                    </div>
                  </Link>
                )}
                <div className="space-y-3">
                  {list.map((t) => (
                    <WalletTicket key={t.id} ticket={t} offline={unreachable} onShow={() => setOpen(t)} onDownload={() => downloadPdf(t)} />
                  ))}
                </div>
              </section>
            )
          })}
        </div>
      )}

      <Modal open={!!open} onClose={() => setOpen(null)} title={open?.event?.title ?? 'Ticket'} description={open?.event ? formatEventDate(open.event.starts_at, open.event.ends_at, open.event.timezone) : undefined} size="sm">
        {open?.qr_payload && (
          <div className="flex flex-col items-center text-center">
            <div className="rounded-2xl bg-white p-3 ring-1 ring-zinc-200"><QRCode value={open.qr_payload} size={256} /></div>
            <p className="mt-4 font-mono text-xl font-semibold tracking-[0.2em] text-ink-900">{open.display_code}</p>
            <p className="text-sm text-zinc-500">{open.ticket_type_name} for {fullName(open.holder_first_name, open.holder_last_name)}</p>
            <p className="mt-4 text-xs text-zinc-500">Turn your screen brightness up for faster scanning. Each ticket admits one person.</p>
          </div>
        )}
      </Modal>
    </div>
  )
}
