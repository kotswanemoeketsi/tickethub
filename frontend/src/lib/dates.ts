// Date ranges for the "when" filters. Ranges are calendar days in the visitor's time zone
// (the API treats `to` as inclusive).

export type When = '' | 'today' | 'weekend' | 'week' | 'month' | 'custom'

export const whenOptions: { value: Exclude<When, 'custom'>; label: string }[] = [
  { value: '', label: 'Any date' },
  { value: 'today', label: 'Today' },
  { value: 'weekend', label: 'This weekend' },
  { value: 'week', label: 'Next 7 days' },
  { value: 'month', label: 'Next 30 days' },
]

export function isoDay(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

function addDays(d: Date, n: number): Date {
  const out = new Date(d)
  out.setDate(out.getDate() + n)
  return out
}

/** Returns the from/to days for a preset, or nothing for "any date". */
export function rangeFor(when: When, now = new Date()): { from?: string; to?: string } {
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  switch (when) {
    case 'today':
      return { from: isoDay(today), to: isoDay(today) }
    case 'weekend': {
      // Friday to Sunday; from Friday onwards, the rest of this weekend.
      const day = today.getDay() // 0 = Sunday, 5 = Friday, 6 = Saturday
      const daysToSunday = day === 0 ? 0 : 7 - day
      const start = day === 0 || day >= 5 ? today : addDays(today, 5 - day)
      return { from: isoDay(start), to: isoDay(addDays(today, daysToSunday)) }
    }
    case 'week':
      return { from: isoDay(today), to: isoDay(addDays(today, 6)) }
    case 'month':
      return { from: isoDay(today), to: isoDay(addDays(today, 29)) }
    default:
      return {}
  }
}
