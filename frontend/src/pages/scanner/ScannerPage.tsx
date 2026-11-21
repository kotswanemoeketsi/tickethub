// The door scanner: a phone-first page for check-in staff. It reads ticket QR codes with
// the camera (or takes a typed code), sends them to the API, and shows a large, colour-coded
// result. All validation happens on the server; the page only displays the verdict.
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import QrScanner from 'qr-scanner'
import { ArrowLeft, Camera, CameraOff, CircleCheck, CircleX, Keyboard, LogIn, LogOut } from 'lucide-react'
import { api, errorMessage } from '../../lib/api'
import { formatDateTime, formatTime, percent } from '../../lib/format'
import type { CheckinStats, ScanResult, ScannableEvent } from '../../lib/types'
import { Alert, Button, cx, EmptyState, Loading, Select } from '../../components/ui'

const EVENT_KEY = 'gotaticket.scanner.event'
const GATE_KEY = 'gotaticket.scanner.gate'
const REPEAT_WINDOW_MS = 3000

function readSetting(key: string) {
  try {
    return localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function saveSetting(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Storage can be unavailable (private mode); the setting simply is not remembered.
  }
}

let audio: AudioContext | null = null

/** A short beep: high for accepted, low for rejected. Plus a vibration where supported. */
function feedback(ok: boolean) {
  try {
    audio ??= new AudioContext()
    const osc = audio.createOscillator()
    const gain = audio.createGain()
    osc.frequency.value = ok ? 880 : 220
    gain.gain.value = 0.15
    osc.connect(gain).connect(audio.destination)
    osc.start()
    osc.stop(audio.currentTime + (ok ? 0.12 : 0.35))
  } catch {
    // Audio is optional.
  }
  navigator.vibrate?.(ok ? 80 : [120, 60, 120])
}

interface Entry {
  key: number
  result: ScanResult
}

// Browsers only allow the camera on HTTPS (or localhost), so say so rather than blaming the device.
function cameraErrorMessage(err: unknown): string {
  if (!window.isSecureContext) return 'The camera only works when the site is opened over HTTPS. Type ticket codes instead.'
  if (String(err).includes('NotAllowed')) return 'Camera access was blocked. Allow the camera in your browser settings, or type codes instead.'
  return 'No camera is available on this device. Type ticket codes instead.'
}

export default function ScannerPage() {
  const [eventId, setEventId] = useState(() => readSetting(EVENT_KEY))
  const [gate, setGate] = useState(() => readSetting(GATE_KEY))
  const [direction, setDirection] = useState<'in' | 'out'>('in')
  const [cameraOn, setCameraOn] = useState(true)
  const [cameraError, setCameraError] = useState('')
  const [manual, setManual] = useState('')
  const [current, setCurrent] = useState<ScanResult | null>(null)
  const [history, setHistory] = useState<Entry[]>([])
  const [stats, setStats] = useState<CheckinStats | null>(null)
  const video = useRef<HTMLVideoElement>(null)
  const lastScan = useRef<{ code: string; at: number }>({ code: '', at: 0 })
  const busy = useRef(false)

  const events = useQuery({ queryKey: ['scanner', 'events'], queryFn: () => api<ScannableEvent[]>('/admin/checkin/events') })
  const event = events.data?.find((e) => String(e.id) === eventId)

  // With a single event on today, there is nothing to choose.
  useEffect(() => {
    const list = events.data ?? []
    if (list.length === 1 && !list.some((e) => String(e.id) === eventId)) setEventId(String(list[0].id))
  }, [events.data, eventId])

  // Initial stats for the chosen event.
  useEffect(() => {
    if (!eventId) return
    let cancelled = false
    api<{ stats: CheckinStats }>(`/admin/events/${eventId}/checkins`, { query: { limit: 1 } })
      .then((r) => !cancelled && setStats(r.stats))
      .catch(() => !cancelled && setStats(null))
    return () => {
      cancelled = true
    }
  }, [eventId])

  const scan = useMutation({
    mutationFn: (code: string) => api<ScanResult>('/admin/checkin/scan', { method: 'POST', body: { event_id: Number(eventId), code, direction, gate } }),
    onSuccess: (result) => {
      setCurrent(result)
      setStats(result.stats)
      setHistory((h) => [{ key: Date.now(), result }, ...h].slice(0, 20))
      feedback(result.result === 'accepted')
    },
    onError: () => feedback(false),
    onSettled: () => {
      busy.current = false
    },
  })

  const submitCode = useCallback(
    (raw: string) => {
      const code = raw.trim()
      if (!code || !eventId || busy.current) return
      // The camera reports the same code many times a second; ignore repeats briefly.
      const now = Date.now()
      if (code === lastScan.current.code && now - lastScan.current.at < REPEAT_WINDOW_MS) return
      lastScan.current = { code, at: now }
      busy.current = true
      scan.mutate(code)
    },
    [eventId, scan],
  )

  // Keep the latest handler without restarting the camera on every render.
  const handler = useRef(submitCode)
  useEffect(() => {
    handler.current = submitCode
  }, [submitCode])

  useEffect(() => {
    if (!cameraOn || !eventId || !video.current) return
    const scanner = new QrScanner(video.current, (r) => handler.current(r.data), {
      preferredCamera: 'environment',
      highlightScanRegion: true,
      highlightCodeOutline: true,
      maxScansPerSecond: 8,
    })
    scanner.start().then(
      () => setCameraError(''),
      (err: unknown) => setCameraError(cameraErrorMessage(err)),
    )
    return () => {
      scanner.stop()
      scanner.destroy()
    }
  }, [cameraOn, eventId])

  const chooseEvent = (id: string) => {
    setEventId(id)
    saveSetting(EVENT_KEY, id)
    setCurrent(null)
    setHistory([])
  }

  const onManual = (e: FormEvent) => {
    e.preventDefault()
    lastScan.current = { code: '', at: 0 } // typed codes are always submitted
    submitCode(manual)
    setManual('')
  }

  if (events.isLoading) return <Loading />

  const accepted = current?.result === 'accepted'
  return (
    <div className="min-h-dvh bg-ink-950 text-white">
      <header className="flex items-center gap-3 border-b border-white/10 px-4 py-3">
        <Link to="/admin" className="rounded-lg p-1.5 text-white/70 hover:bg-white/10" aria-label="Back to the portal">
          <ArrowLeft className="size-5" />
        </Link>
        <div className="min-w-0 flex-1">
          <p className="truncate font-display text-lg font-semibold">{event?.title ?? 'Ticket scanner'}</p>
          {event && <p className="truncate text-xs text-white/60">{event.venue}, {formatDateTime(event.starts_at, event.timezone)}</p>}
        </div>
        {stats && (
          <div className="text-right text-xs">
            <p className="text-lg font-bold leading-none tabular-nums">{stats.checked_in}<span className="text-white/50">/{stats.tickets_issued}</span></p>
            <p className="text-white/60">{percent(stats.checked_in, stats.tickets_issued)} in, {stats.currently_inside} inside</p>
          </div>
        )}
      </header>

      <main className="mx-auto max-w-md space-y-4 p-4">
        {!events.data?.length ? (
          <div className="rounded-2xl bg-white text-zinc-900"><EmptyState title="No events to scan" description="Events appear here on the day they take place." /></div>
        ) : (
          <div className="grid grid-cols-[1fr_110px] gap-2 [&_label]:text-white/70">
            <Select
              label="Event"
              placeholder="Choose an event"
              value={eventId}
              onChange={(e) => chooseEvent(e.target.value)}
              options={events.data.map((e) => ({ value: e.id, label: `${e.title}, ${formatTime(e.starts_at, e.timezone)}` }))}
            />
            <div>
              <label htmlFor="gate" className="mb-1.5 block text-sm font-medium">Gate</label>
              <input
                id="gate"
                value={gate}
                onChange={(e) => {
                  setGate(e.target.value)
                  saveSetting(GATE_KEY, e.target.value)
                }}
                placeholder="Main"
                className="h-10 w-full rounded-lg border-0 bg-white px-3 text-sm text-zinc-900"
              />
            </div>
          </div>
        )}

        {eventId && (
          <>
            <div className="grid grid-cols-2 gap-1 rounded-xl bg-white/10 p-1">
              {(['in', 'out'] as const).map((d) => (
                <button
                  key={d}
                  onClick={() => setDirection(d)}
                  className={cx('flex h-10 items-center justify-center gap-2 rounded-lg text-sm font-medium', direction === d ? 'bg-white text-zinc-900' : 'text-white/70')}
                >
                  {d === 'in' ? <LogIn className="size-4" /> : <LogOut className="size-4" />} {d === 'in' ? 'Check in' : 'Check out'}
                </button>
              ))}
            </div>

            <div className="relative overflow-hidden rounded-2xl bg-black">
              {cameraOn ? (
                <video ref={video} className="aspect-square w-full object-cover" muted playsInline />
              ) : (
                <div className="flex aspect-[2/1] items-center justify-center text-sm text-white/50">Camera off</div>
              )}
              <button
                onClick={() => setCameraOn(!cameraOn)}
                className="absolute right-3 top-3 rounded-full bg-black/60 p-2 text-white"
                aria-label={cameraOn ? 'Turn camera off' : 'Turn camera on'}
              >
                {cameraOn ? <CameraOff className="size-5" /> : <Camera className="size-5" />}
              </button>
            </div>
            {cameraError && <Alert tone="warning">{cameraError}</Alert>}

            <form onSubmit={onManual} className="flex gap-2">
              <div className="relative flex-1">
                <Keyboard className="absolute left-3 top-2.5 size-5 text-zinc-400" />
                <input
                  value={manual}
                  onChange={(e) => setManual(e.target.value.toUpperCase())}
                  placeholder="Type a ticket code"
                  autoCapitalize="characters"
                  autoComplete="off"
                  className="h-10 w-full rounded-lg border-0 bg-white pl-10 pr-3 font-mono text-sm tracking-wider text-zinc-900"
                  aria-label="Ticket code"
                />
              </div>
              <Button type="submit" loading={scan.isPending} disabled={!manual.trim()}>Check</Button>
            </form>

            {scan.error && <Alert tone="error">{errorMessage(scan.error)}</Alert>}

            {current && (
              <div className={cx('rounded-2xl p-5', accepted ? 'bg-emerald-600' : 'bg-red-600')} role="status" aria-live="assertive">
                <div className="flex items-center gap-3">
                  {accepted ? <CircleCheck className="size-10 shrink-0" /> : <CircleX className="size-10 shrink-0" />}
                  <div>
                    <p className="text-2xl font-bold">{accepted ? (current.direction === 'in' ? 'Welcome in' : 'Checked out') : 'Do not admit'}</p>
                    <p className="text-white/90">{current.message}</p>
                  </div>
                </div>
                {current.ticket && (
                  <div className="mt-4 rounded-xl bg-black/15 p-3 text-sm">
                    <p className="text-lg font-semibold">{current.ticket.holder_name}</p>
                    <p>{current.ticket.ticket_type_name}{current.ticket.is_complimentary && ' (complimentary)'}</p>
                    <p className="font-mono text-white/80">{current.ticket.display_code}, {current.ticket.order_reference}</p>
                    {current.ticket.first_checked_in_at && !accepted && event && (
                      <p className="mt-1 text-white/80">First scanned {formatTime(current.ticket.first_checked_in_at, event.timezone)}</p>
                    )}
                  </div>
                )}
              </div>
            )}

            {history.length > 1 && (
              <div>
                <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-white/50">Recent scans</p>
                <ul className="divide-y divide-white/10 rounded-xl bg-white/5">
                  {history.slice(1).map(({ key, result }) => (
                    <li key={key} className="flex items-center gap-3 px-3 py-2 text-sm">
                      <span className={cx('size-2.5 shrink-0 rounded-full', result.result === 'accepted' ? 'bg-emerald-400' : 'bg-red-400')} />
                      <span className="min-w-0 flex-1 truncate">{result.ticket?.holder_name ?? result.message}</span>
                      <span className="text-xs text-white/50">{event ? formatTime(result.scanned_at, event.timezone) : ''}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </>
        )}
      </main>
    </div>
  )
}
