import { describe, expect, it } from 'vitest'
import { allProvinces, saProvinces, withEvents } from './locations'
import type { LocationProvince } from './types'

const city = (name: string, event_count: number) => ({ name, event_count, latitude: null, longitude: null })

const fromApi: LocationProvince[] = [
  { name: 'Gauteng', event_count: 2, cities: [city('Johannesburg', 2), city('Soweto', 0)] },
  { name: 'Other places', event_count: 1, cities: [city('Windhoek', 1)] },
  { name: 'Western Cape', event_count: 0, cities: [] },
]

describe('allProvinces', () => {
  it('lists all nine provinces, even before any events exist', () => {
    expect(allProvinces([]).map((p) => p.name)).toEqual(saProvinces)
    expect(allProvinces(undefined).every((p) => p.event_count === 0 && p.cities.length === 0)).toBe(true)
  })

  it('keeps counts, drops cities without events and puts other places last', () => {
    const provinces = allProvinces(fromApi)
    expect(provinces.map((p) => p.name)).toEqual([...saProvinces, 'Other places'])
    expect(provinces.find((p) => p.name === 'Gauteng')).toMatchObject({ event_count: 2, cities: [city('Johannesburg', 2)] })
  })

  it('leaves out places outside the nine when they have no events', () => {
    const quiet = fromApi.map((p) => (p.name === 'Other places' ? { ...p, event_count: 0 } : p))
    expect(allProvinces(quiet).map((p) => p.name)).toEqual(saProvinces)
  })
})

describe('withEvents', () => {
  it('keeps only provinces and cities with events, for filters', () => {
    expect(withEvents(fromApi).map((p) => p.name)).toEqual(['Gauteng', 'Other places'])
  })
})
