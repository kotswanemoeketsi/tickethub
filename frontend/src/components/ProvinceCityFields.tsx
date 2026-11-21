import { useMemo } from 'react'
import { saProvinces, usePlaces } from '../lib/locations'
import { CityCombobox, type CityOption } from './CityCombobox'
import { Select } from './ui'

export interface ProvinceCity {
  province: string
  city: string
}

/**
 * A province dropdown, then a searchable dropdown of that province's cities and towns in
 * alphabetical order. extraCities adds names the list may lack, such as the towns of
 * existing venues; allowOther lets the user keep a town that isn't listed.
 */
export function ProvinceCityFields({ value, onChange, extraCities = [], allowOther = false, errors = {}, disabled = false }: {
  value: ProvinceCity
  /** place carries the coordinates when the city came from the list. */
  onChange: (next: ProvinceCity, place?: CityOption) => void
  extraCities?: { province: string; city: string }[]
  allowOther?: boolean
  errors?: { province?: string; city?: string }
  disabled?: boolean
}) {
  const places = usePlaces(value.province)
  const options = useMemo(() => {
    const byKey = new Map<string, CityOption>()
    for (const p of places.data ?? []) byKey.set(p.name.toLowerCase(), p)
    for (const c of extraCities) {
      if (c.province === value.province && c.city && !byKey.has(c.city.toLowerCase())) byKey.set(c.city.toLowerCase(), { name: c.city })
    }
    return [...byKey.values()].sort((a, b) => a.name.localeCompare(b.name, 'en-ZA', { sensitivity: 'base' }))
  }, [places.data, extraCities, value.province])

  return (
    <>
      <Select
        label="Province"
        placeholder="Choose a province"
        options={saProvinces.map((p) => ({ value: p, label: p }))}
        value={value.province}
        onChange={(e) => onChange({ province: e.target.value, city: '' })}
        error={errors.province}
        disabled={disabled}
      />
      <CityCombobox
        label="City"
        value={value.city}
        options={options}
        onChange={(city, place) => onChange({ province: value.province, city }, place)}
        placeholder={value.province ? 'Search or choose a city' : 'Choose a province first'}
        disabled={disabled || !value.province}
        loading={places.isFetching}
        error={errors.city ?? (places.isError ? "Couldn't load the cities. Try again." : undefined)}
        allowCustom={allowOther}
      />
    </>
  )
}
