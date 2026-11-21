import { describe, expect, it } from 'vitest'
import { rangeFor } from './dates'

describe('rangeFor', () => {
  it('covers Friday to Sunday for a weekday', () => {
    // Wednesday 18 November 2026
    expect(rangeFor('weekend', new Date(2026, 10, 18, 10))).toEqual({ from: '2026-11-20', to: '2026-11-22' })
  })

  it('keeps the rest of the weekend on Saturday and Sunday', () => {
    expect(rangeFor('weekend', new Date(2026, 10, 21, 10))).toEqual({ from: '2026-11-21', to: '2026-11-22' })
    expect(rangeFor('weekend', new Date(2026, 10, 22, 10))).toEqual({ from: '2026-11-22', to: '2026-11-22' })
  })

  it('crosses month ends for rolling ranges', () => {
    expect(rangeFor('week', new Date(2026, 10, 28))).toEqual({ from: '2026-11-28', to: '2026-12-04' })
    expect(rangeFor('')).toEqual({})
  })
})
