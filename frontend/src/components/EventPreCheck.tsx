import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2, ShieldAlert, Sparkles } from 'lucide-react'
import { ApiError, api, errorMessage } from '../lib/api'
import type { EventReview, ReviewIssue } from '../lib/ai'
import { formatDateTime } from '../lib/format'
import { Alert, Badge, Button, Card, CardHeader } from './ui'

const verdicts: Record<EventReview['verdict'], { label: string; tone: 'green' | 'yellow' | 'red'; icon: ReactNode }> = {
  ready: { label: 'Looks ready', tone: 'green', icon: <CheckCircle2 className="size-5 text-emerald-600" /> },
  needs_changes: { label: 'Needs changes', tone: 'yellow', icon: <AlertTriangle className="size-5 text-amber-600" /> },
  high_risk: { label: 'High risk', tone: 'red', icon: <ShieldAlert className="size-5 text-red-600" /> },
}

const severityTone: Record<ReviewIssue['severity'], 'red' | 'yellow' | 'gray'> = { high: 'red', medium: 'yellow', low: 'gray' }
const areaLabel: Record<string, string> = {
  details: 'Details', description: 'Description', dates: 'Dates', tickets: 'Tickets', refunds: 'Refunds', risk: 'Risk',
}

/**
 * The AI pre-check of an event awaiting approval. It points the reviewer at problems; the
 * reviewer still decides, and the check is never run automatically.
 */
export function EventPreCheck({ eventId }: { eventId: number }) {
  const queryClient = useQueryClient()
  const key = ['admin', 'event', String(eventId), 'ai-review']
  const latest = useQuery({
    queryKey: key,
    queryFn: () => api<EventReview>(`/admin/ai/events/${eventId}/review`),
    // No check yet is a normal state, not an error to retry.
    retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 2,
  })
  const run = useMutation({
    mutationFn: () => api<EventReview>(`/admin/ai/events/${eventId}/review`, { method: 'POST' }),
    onSuccess: (review) => queryClient.setQueryData(key, review),
  })
  const review = latest.data
  const notChecked = latest.error instanceof ApiError && latest.error.status === 404
  const v = review && verdicts[review.verdict]

  return (
    <Card>
      <CardHeader
        title={<span className="flex items-center gap-2"><Sparkles className="size-4 text-brand-600" /> AI pre-check</span>}
        description="Points out problems before you approve. It's advice: you make the decision."
        actions={
          <Button size="sm" variant={review ? 'secondary' : 'primary'} loading={run.isPending} onClick={() => run.mutate()}>
            {review ? 'Check again' : 'Run pre-check'}
          </Button>
        }
      />
      <div className="space-y-4 p-5">
        {run.error && <Alert tone="error">{errorMessage(run.error)}</Alert>}
        {latest.isLoading && <p className="text-sm text-zinc-500">Loading…</p>}
        {notChecked && !review && !run.isPending && (
          <p className="text-sm text-zinc-500">Not checked yet. The check reads the listing, ticket types and refund settings, and takes about half a minute.</p>
        )}
        {run.isPending && <p className="text-sm text-zinc-500">Reading the listing and ticket types…</p>}
        {review && v && (
          <>
            {review.stale && <Alert tone="warning">The event has changed since this check. Run it again to include the changes.</Alert>}
            <div className="flex items-start gap-3">
              {v.icon}
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone={v.tone}>{v.label}</Badge>
                  <span className="text-xs text-zinc-500">Checked {formatDateTime(review.checked_at)}</span>
                </div>
                <p className="mt-2 text-[15px] text-zinc-800">{review.summary}</p>
              </div>
            </div>
            {review.issues.length > 0 && (
              <ul className="divide-y divide-zinc-100 rounded-xl ring-1 ring-zinc-200">
                {review.issues.map((i, n) => (
                  <li key={n} className="flex items-start gap-3 px-4 py-3 text-sm">
                    <Badge tone={severityTone[i.severity]}>{i.severity === 'high' ? 'High' : i.severity === 'medium' ? 'Medium' : 'Low'}</Badge>
                    <div className="min-w-0">
                      <p className="font-medium text-zinc-900">{areaLabel[i.area] ?? i.area}</p>
                      <p className="mt-0.5 text-zinc-700">{i.message}</p>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
    </Card>
  )
}
