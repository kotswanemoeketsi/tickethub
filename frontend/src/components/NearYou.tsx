import { useState, type ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { LocateFixed, MapPin } from 'lucide-react'
import { api } from '../lib/api'
import { useArea, type Area, type LocateState } from '../lib/locations'
import type { PublicEvent } from '../lib/types'
import { EventCarousel } from './events'
import { ProvinceCityFields, type ProvinceCity } from './ProvinceCityFields'
import { Button, ButtonLink, cx } from './ui'

const radiusKM = 200

const locateHelp: Partial<Record<LocateState, string>> = {
  denied: 'Location access is blocked for this site, so choose your city instead.',
  unavailable: "We couldn't find your location. Choose your city instead.",
}

/** The two ways to set the area: the device's location, or a city from the list. */
function AreaChooser({ locate, state, onChoose }: {
  locate: () => void
  state: LocateState
  onChoose: (area: Area) => void
}) {
  const [place, setPlace] = useState<ProvinceCity>({ province: '', city: '' })
  return (
    <div>
      <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
        <Button icon={<LocateFixed className="size-4" />} loading={state === 'locating'} onClick={locate} className="h-11 shrink-0">
          Use my location
        </Button>
        <span className="text-sm text-zinc-500 lg:pb-3">or choose</span>
        <div className="grid gap-3 sm:grid-cols-2 lg:w-[30rem]">
          <ProvinceCityFields
            value={place}
            onChange={(next, option) => {
              setPlace(next)
              if (option?.latitude != null && option.longitude != null) {
                onChoose({ label: option.name, latitude: option.latitude, longitude: option.longitude, source: 'city' })
              }
            }}
          />
        </div>
      </div>
      {locateHelp[state] && <p className="mt-3 text-sm text-amber-800">{locateHelp[state]}</p>}
    </div>
  )
}

function Panel({ children, stacked = false }: { children: ReactNode; stacked?: boolean }) {
  return (
    <div className={cx('flex flex-col gap-5 rounded-2xl bg-white p-6 ring-1 ring-zinc-200 sm:p-8', !stacked && 'lg:flex-row lg:items-center lg:justify-between')}>
      {children}
    </div>
  )
}

/** Upcoming events within 200 km of the buyer, soonest first. */
export function NearYou() {
  const { area, setArea, locate, state } = useArea()
  const [changing, setChanging] = useState(false)

  const nearby = useQuery({
    queryKey: ['public', 'nearby', area?.latitude, area?.longitude],
    queryFn: () => api<PublicEvent[]>('/public/nearby', { query: { lat: area!.latitude, lng: area!.longitude, radius_km: radiusKM } }),
    enabled: !!area,
  })
  const choose = (next: Area) => {
    setArea(next)
    setChanging(false)
  }
  const chooser = <AreaChooser locate={locate} state={state} onChoose={choose} />

  if (!area) {
    return (
      <EventCarousel
        title="Near you"
        description={`Events within ${radiusKM} km of where you are.`}
        events={[]}
        empty={
          <Panel stacked>
            <div className="flex items-start gap-4">
              <span className="grid size-11 shrink-0 place-items-center rounded-full bg-brand-50 text-brand-700"><MapPin className="size-5" /></span>
              <div>
                <p className="font-display text-lg font-semibold text-ink-900">See what's on in your area</p>
                <p className="mt-1 text-sm text-zinc-500">Share your location once, or pick your city. We only use it to sort events by distance.</p>
              </div>
            </div>
            {chooser}
          </Panel>
        }
      />
    )
  }

  return (
    <EventCarousel
      title="Near you"
      description={<>Events within {radiusKM} km of <span className="font-medium text-ink-900">{area.label}</span>.</>}
      action={
        <button
          type="button"
          onClick={() => setChanging(!changing)}
          aria-expanded={changing}
          className={cx('text-sm font-semibold hover:text-brand-800', changing ? 'text-brand-800' : 'text-brand-700')}
        >
          Change area
        </button>
      }
      toolbar={changing && <div className="mb-5 rounded-2xl bg-white p-5 ring-1 ring-zinc-200">{chooser}</div>}
      events={nearby.data ?? []}
      isLoading={nearby.isLoading}
      empty={
        <Panel>
          <div>
            <p className="font-display text-lg font-semibold text-ink-900">Nothing on within {radiusKM} km of {area.label} yet</p>
            <p className="mt-1 text-sm text-zinc-500">Try another city, or browse everything that's on.</p>
          </div>
          <ButtonLink to="/events" variant="secondary" className="shrink-0">Browse all events</ButtonLink>
        </Panel>
      }
    />
  )
}
