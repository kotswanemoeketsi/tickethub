import { useQuery } from '@tanstack/react-query'
import { api } from './api'

export interface Features {
  ai_search: boolean
  ai_event_writer: boolean
  ai_event_review: boolean
}

const off: Features = { ai_search: false, ai_event_writer: false, ai_event_review: false }

/** Which AI features the server has switched on. Everything is off until it answers. */
export function useFeatures(): Features {
  const { data } = useQuery({ queryKey: ['public', 'features'], queryFn: () => api<Features>('/public/features'), staleTime: 5 * 60_000 })
  return data ?? off
}

export interface SearchFilters {
  keywords: string
  category: string
  province: string
  city: string
  when: '' | 'today' | 'weekend' | 'week' | 'month' | 'custom'
  from: string
  to: string
  max_price: number
  sort: '' | 'price' | 'popular'
}

/**
 * Searches of three or more words are worth interpreting ("jazz in Cape Town this
 * Saturday"); one or two words are names and go straight to the keyword search.
 */
export function wantsSmartSearch(query: string): boolean {
  return query.trim().split(/\s+/).length >= 3
}

/** Browse-page parameters for interpreted filters; the original text is kept as "asked". */
export function filtersToParams(f: SearchFilters, asked: string): URLSearchParams {
  const p = new URLSearchParams()
  const set = (k: string, v: string | number) => {
    if (v) p.set(k, String(v))
  }
  set('q', f.keywords)
  set('category', f.category)
  if (f.city) set('city', f.city)
  else set('province', f.province)
  set('when', f.when)
  if (f.when === 'custom') {
    set('from', f.from)
    set('to', f.to)
  }
  set('max_price', f.max_price)
  set('sort', f.sort)
  set('asked', asked)
  return p
}

export interface EventDraft {
  title: string
  summary: string
  description: string
  category: string
  to_add?: string[]
}

export interface ReviewIssue {
  severity: 'high' | 'medium' | 'low'
  area: string
  message: string
}

export interface EventReview {
  verdict: 'ready' | 'needs_changes' | 'high_risk'
  summary: string
  issues: ReviewIssue[]
  model: string
  checked_at: string
  stale: boolean
}
