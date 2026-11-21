// Formatting helpers. Money follows the same convention as the API's PDFs and emails
// ("R 1,250.00"), and dates are always shown in the event's own time zone.

const symbols: Record<string, string> = { ZAR: 'R', USD: '$', EUR: '€', GBP: '£', BWP: 'P', NAD: 'N$' }

export function formatMoney(cents: number, currency = 'ZAR'): string {
  const negative = cents < 0
  const abs = Math.abs(Math.round(cents))
  const whole = Math.floor(abs / 100).toLocaleString('en-US')
  const fraction = String(abs % 100).padStart(2, '0')
  const prefix = symbols[currency.toUpperCase()] ?? currency.toUpperCase()
  return `${negative ? '-' : ''}${prefix} ${whole}.${fraction}`
}

/** Converts a rand amount typed by a user ("1250.50") into cents. */
export function toCents(value: string): number {
  const n = Number(value.replace(/[^0-9.]/g, ''))
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

/** Converts cents into an editable amount string ("1250.50"). */
export function fromCents(cents: number): string {
  return (cents / 100).toFixed(2)
}

function formatter(timeZone: string | undefined, options: Intl.DateTimeFormatOptions) {
  return new Intl.DateTimeFormat('en-GB', { timeZone, ...options })
}

export function formatDate(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return '—'
  return formatter(timeZone, { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(iso))
}

export function formatDateTime(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return '—'
  return formatter(timeZone, { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(iso))
}

export function formatTime(iso: string | null | undefined, timeZone?: string): string {
  if (!iso) return '—'
  return formatter(timeZone, { hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(iso))
}

/** "Sat, 5 Dec 2026, 18:00–23:30" in the event's time zone. */
export function formatEventDate(startsAt: string, endsAt: string, timeZone: string): string {
  const day = formatter(timeZone, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric' })
  const start = new Date(startsAt)
  const end = new Date(endsAt)
  const sameDay = day.format(start) === day.format(end)
  if (sameDay) return `${day.format(start)}, ${formatTime(startsAt, timeZone)}–${formatTime(endsAt, timeZone)}`
  return `${day.format(start)} ${formatTime(startsAt, timeZone)} – ${day.format(end)} ${formatTime(endsAt, timeZone)}`
}

/** Short date parts for event cards. */
export function dateBadge(iso: string, timeZone: string): { day: string; month: string } {
  const d = new Date(iso)
  return {
    day: formatter(timeZone, { day: 'numeric' }).format(d),
    month: formatter(timeZone, { month: 'short' }).format(d).toUpperCase(),
  }
}

/** "pending_review" → "Pending review". */
export function humanize(value: string): string {
  if (!value) return ''
  const s = value.replace(/_/g, ' ')
  return s.charAt(0).toUpperCase() + s.slice(1)
}

export function percent(part: number, whole: number): string {
  if (!whole) return '0%'
  return `${Math.round((part / whole) * 100)}%`
}

export function fullName(first: string, last: string): string {
  return `${first} ${last}`.trim()
}

/** Minutes and seconds remaining until iso, e.g. "12:05". */
export function countdown(iso: string, now: number): string {
  const ms = Math.max(0, new Date(iso).getTime() - now)
  const minutes = Math.floor(ms / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}
