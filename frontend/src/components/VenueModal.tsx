import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, errorMessage, fieldErrors } from '../lib/api'
import type { Venue } from '../lib/types'
import { ProvinceCityFields } from './ProvinceCityFields'
import { useToast } from './toast'
import { Alert, Button, Input, Modal, Select } from './ui'

/** Creates or edits a venue. initial prefills a new venue, for example from the event form. */
export function VenueModal({ venue, organisers, allowShared, initial, onSaved, onClose }: {
  venue: Venue | null
  organisers: { value: number; label: string }[]
  allowShared: boolean
  /** Prefills a new venue; an organiser given here is fixed and its picker hidden. */
  initial?: { organiser_id?: string; province?: string; city?: string }
  onSaved?: (venue: Venue) => void
  onClose: () => void
}) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const fixedOwner = !venue && !!initial?.organiser_id
  const [form, setForm] = useState({
    organiser_id:
      initial?.organiser_id ?? (venue?.organiser_id ? String(venue.organiser_id) : organisers.length === 1 && !allowShared ? String(organisers[0].value) : ''),
    name: venue?.name ?? '',
    address_line: venue?.address_line ?? '',
    city: venue?.city ?? initial?.city ?? '',
    province: venue?.province ?? initial?.province ?? '',
    country: venue?.country ?? 'South Africa',
    postal_code: venue?.postal_code ?? '',
    capacity: venue?.capacity != null ? String(venue.capacity) : '',
    latitude: venue?.latitude != null ? String(venue.latitude) : '',
    longitude: venue?.longitude != null ? String(venue.longitude) : '',
  })
  const inSouthAfrica = form.country.trim().toLowerCase() === 'south africa'
  const notANumber = (v: string) => (v.trim() !== '' && !Number.isFinite(Number(v)) ? 'Enter a number, like -26.2041' : undefined)
  const pinError = { latitude: notANumber(form.latitude), longitude: notANumber(form.longitude) }
  const save = useMutation({
    mutationFn: () => {
      const optional = (v: string) => (v.trim() === '' ? null : Number(v))
      const body = {
        ...form,
        organiser_id: Number(form.organiser_id) || 0,
        capacity: optional(form.capacity),
        latitude: optional(form.latitude),
        longitude: optional(form.longitude),
      }
      return venue ? api<Venue>(`/admin/venues/${venue.id}`, { method: 'PUT', body }) : api<Venue>('/admin/venues', { method: 'POST', body })
    },
    onSuccess: (saved) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'venues'] })
      queryClient.invalidateQueries({ queryKey: ['public', 'locations'] })
      toast.success('Venue saved.')
      onSaved?.(saved)
      onClose()
    },
  })
  const errors = fieldErrors(save.error)
  // A new city or province means a new place: drop the old pin so the server finds the new one.
  const update = (changes: Partial<typeof form>) =>
    setForm((f) => {
      const moved = (changes.city !== undefined && changes.city !== f.city) || (changes.province !== undefined && changes.province !== f.province)
      return { ...f, ...changes, ...(moved ? { latitude: '', longitude: '' } : {}) }
    })
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => update({ [k]: e.target.value })

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={venue ? 'Edit venue' : 'New venue'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={save.isPending} disabled={!!(pinError.latitude || pinError.longitude)} onClick={() => save.mutate()}>Save</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {save.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(save.error)}</Alert></div>}
        {!venue && !fixedOwner && (
          <Select
            label="Owner"
            className="sm:col-span-2"
            placeholder={allowShared ? 'Shared with all organisers' : 'Choose an organiser'}
            options={organisers}
            value={form.organiser_id}
            onChange={set('organiser_id')}
            error={errors.organiser_id}
          />
        )}
        <Input label="Name" className="sm:col-span-2" value={form.name} onChange={set('name')} error={errors.name} autoFocus={fixedOwner} />
        <Input label="Street address" className="sm:col-span-2" value={form.address_line} onChange={set('address_line')} error={errors.address_line} />
        {inSouthAfrica ? (
          <ProvinceCityFields
            value={{ province: form.province, city: form.city }}
            onChange={(next) => update(next)}
            allowOther
            errors={{ province: errors.province, city: errors.city }}
          />
        ) : (
          <>
            <Input label="Province or region" value={form.province} onChange={set('province')} error={errors.province} />
            <Input label="City" value={form.city} onChange={set('city')} error={errors.city} />
          </>
        )}
        <Input label="Country" value={form.country} onChange={set('country')} error={errors.country} />
        <Input label="Postal code" value={form.postal_code} onChange={set('postal_code')} error={errors.postal_code} />
        <Input label="Capacity (optional)" type="number" min={1} value={form.capacity} onChange={set('capacity')} error={errors.capacity} />
        <div className="hidden sm:block" />
        <Input
          label="Latitude (optional)"
          inputMode="decimal"
          placeholder="-26.2041"
          value={form.latitude}
          onChange={set('latitude')}
          error={errors.latitude ?? pinError.latitude}
          hint="Leave both empty to use the city centre."
        />
        <Input label="Longitude (optional)" inputMode="decimal" placeholder="28.0473" value={form.longitude} onChange={set('longitude')} error={errors.longitude ?? pinError.longitude} />
      </div>
    </Modal>
  )
}
