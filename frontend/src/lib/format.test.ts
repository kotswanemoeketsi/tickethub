import { describe, expect, it } from 'vitest'
import { countdown, formatEventDate, formatMoney, fromCents, humanize, toCents } from './format'

describe('formatMoney', () => {
  it('matches the API convention', () => {
    expect(formatMoney(0)).toBe('R 0.00')
    expect(formatMoney(125000)).toBe('R 1,250.00')
    expect(formatMoney(-9950)).toBe('-R 99.50')
    expect(formatMoney(100, 'KES')).toBe('KES 1.00')
  })
})

describe('amount conversion', () => {
  it('round-trips rand amounts', () => {
    expect(toCents('1250.5')).toBe(125050)
    expect(toCents('R 99.99')).toBe(9999)
    expect(fromCents(125050)).toBe('1250.50')
  })
})

describe('dates', () => {
  it('formats events in their own time zone', () => {
    const text = formatEventDate('2026-12-05T16:00:00Z', '2026-12-05T21:30:00Z', 'Africa/Johannesburg')
    expect(text).toContain('18:00')
    expect(text).toContain('23:30')
    expect(text).toContain('5 Dec 2026')
  })
  it('counts down reservations', () => {
    const now = Date.parse('2026-01-01T10:00:00Z')
    expect(countdown('2026-01-01T10:12:05Z', now)).toBe('12:05')
    expect(countdown('2026-01-01T09:00:00Z', now)).toBe('0:00')
  })
  it('humanizes statuses', () => {
    expect(humanize('pending_review')).toBe('Pending review')
  })
})
