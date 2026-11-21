import { useCallback, useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from './api'
import type { LocationProvince } from './types'

/** The nine provinces, alphabetically, as the API stores them. */
export const saProvinces = [
  'Eastern Cape', 'Free State', 'Gauteng', 'KwaZulu-Natal', 'Limpopo', 'Mpumalanga', 'North West', 'Northern Cape', 'Western Cape',
]

/** Provinces and cities with upcoming events, in alphabetical order. */
export function useLocations() {
  return useQuery({ queryKey: ['public', 'locations'], queryFn: () => api<LocationProvince[]>('/public/locations'), staleTime: 300_000 })
}

export interface Place {
  name: string
  former_names?: string[]
  latitude: number
  longitude: number
}

/** Every city, town and settlement in a province, alphabetically. It rarely changes. */
export function usePlaces(province: string) {
  return useQuery({
    queryKey: ['public', 'places', province],
    queryFn: () => api<Place[]>('/public/places', { query: { province } }),
    enabled: !!province,
    staleTime: Infinity,
    gcTime: 60 * 60_000,
  })
}

/** Keeps only the provinces and cities that have upcoming events, for filters. */
export function withEvents(provinces: LocationProvince[] | undefined): LocationProvince[] {
  return (provinces ?? [])
    .filter((p) => p.event_count > 0)
    .map((p) => ({ ...p, cities: p.cities.filter((c) => c.event_count > 0) }))
}

/** Where "near you" is measured from: the device's position or a city the buyer chose. */
export interface Area {
  label: string
  latitude: number
  longitude: number
  source: 'device' | 'city'
}

export type LocateState = 'idle' | 'locating' | 'denied' | 'unavailable'

const storageKey = 'gotaticket.area'

function readArea(): Area | null {
  try {
    const raw = localStorage.getItem(storageKey)
    if (!raw) return null
    const a = JSON.parse(raw) as Area
    return Number.isFinite(a.latitude) && Number.isFinite(a.longitude) && a.label ? a : null
  } catch {
    return null
  }
}

function writeArea(area: Area | null) {
  try {
    if (area) localStorage.setItem(storageKey, JSON.stringify(area))
    else localStorage.removeItem(storageKey)
  } catch {
    // Storage can be unavailable (private browsing); the choice then lasts for this visit.
  }
}

// About a kilometre: enough for "within 200 km", and the exact position is never kept.
const round = (n: number) => Math.round(n * 100) / 100

/**
 * The buyer's area for nearby events. A previous choice is remembered on this device; if
 * the browser already allows location access, the position is refreshed silently, and
 * otherwise the buyer is only asked when they press "Use my location".
 */
export function useArea() {
  const [area, setAreaState] = useState<Area | null>(readArea)
  const [state, setState] = useState<LocateState>('idle')

  const setArea = useCallback((next: Area | null) => {
    setAreaState(next)
    writeArea(next)
  }, [])

  const locate = useCallback(() => {
    if (!('geolocation' in navigator)) {
      setState('unavailable')
      return
    }
    setState('locating')
    navigator.geolocation.getCurrentPosition(
      (pos) => {
        setArea({ label: 'your location', latitude: round(pos.coords.latitude), longitude: round(pos.coords.longitude), source: 'device' })
        setState('idle')
      },
      (err) => setState(err.code === err.PERMISSION_DENIED ? 'denied' : 'unavailable'),
      { enableHighAccuracy: false, timeout: 10_000, maximumAge: 600_000 },
    )
  }, [setArea])

  useEffect(() => {
    if (area?.source === 'city' || !navigator.permissions?.query) return
    let cancelled = false
    navigator.permissions
      .query({ name: 'geolocation' })
      .then((status) => {
        if (!cancelled && status.state === 'granted') locate()
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
    // Only on first render: later changes come from the buyer's own actions.
  }, [])

  return { area, setArea, locate, state }
}
