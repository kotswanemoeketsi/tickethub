import { useQuery } from '@tanstack/react-query'
import { apiPage } from '../lib/api'
import { formatDate } from '../lib/format'
import type { AdminEvent } from '../lib/types'
import { Select } from './ui'

/**
 * Chooses one event to narrow a view to. Lists the events the user can see that have been
 * on sale (published or cancelled), soonest first, so drafts never clutter the list.
 */
export function EventPicker({ value, onChange, label = 'Event', allowAll = true, className }: {
  value: string
  onChange: (eventId: string) => void
  label?: string
  allowAll?: boolean
  className?: string
}) {
  const events = useQuery({
    queryKey: ['admin', 'events', 'picker'],
    queryFn: () => apiPage<AdminEvent>('/admin/events', { per_page: 100 }),
    staleTime: 60_000,
  })
  const options = (events.data?.data ?? [])
    .filter((e) => e.status === 'published' || e.status === 'cancelled')
    .sort((a, b) => a.starts_at.localeCompare(b.starts_at))
    .map((e) => ({ value: e.id, label: `${e.title} (${formatDate(e.starts_at, e.timezone)})${e.status === 'cancelled' ? ', cancelled' : ''}` }))
  return (
    <Select
      label={label}
      className={className}
      placeholder={allowAll ? 'All events' : 'Choose an event'}
      options={options}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={events.isLoading}
    />
  )
}
