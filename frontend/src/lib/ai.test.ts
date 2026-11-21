import { describe, expect, it } from 'vitest'
import { filtersToParams, wantsSmartSearch, type SearchFilters } from './ai'

const base: SearchFilters = { keywords: '', category: '', province: '', city: '', when: '', from: '', to: '', max_price: 0, sort: '' }

describe('smart search', () => {
  it('only interprets searches that read like sentences', () => {
    expect(wantsSmartSearch('Black Coffee')).toBe(false)
    expect(wantsSmartSearch('  jazz   tonight ')).toBe(false)
    expect(wantsSmartSearch('comedy in Durban')).toBe(true)
  })

  it('maps filters to browse-page parameters', () => {
    const p = filtersToParams({ ...base, category: 'comedy', city: 'Durban', province: 'KwaZulu-Natal', when: 'weekend', from: '2026-11-20', to: '2026-11-22', max_price: 300 }, 'comedy in Durban this weekend under R300')
    // A city is more specific than its province, and presets carry their own dates.
    expect(Object.fromEntries(p)).toEqual({ category: 'comedy', city: 'Durban', when: 'weekend', max_price: '300', asked: 'comedy in Durban this weekend under R300' })
    const custom = filtersToParams({ ...base, keywords: 'Black Coffee', province: 'Gauteng', when: 'custom', from: '2026-12-12', to: '2026-12-12', sort: 'price' }, 'cheap Black Coffee shows in Gauteng on 12 December')
    expect(Object.fromEntries(custom)).toMatchObject({ q: 'Black Coffee', province: 'Gauteng', when: 'custom', from: '2026-12-12', to: '2026-12-12', sort: 'price' })
  })
})
