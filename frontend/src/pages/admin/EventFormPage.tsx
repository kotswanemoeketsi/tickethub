import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ImagePlus, Plus, Trash2 } from 'lucide-react'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { P } from '../../lib/permissions'
import type { AdminEvent, Category, Organiser, Venue } from '../../lib/types'
import { Alert, Button, Card, CardHeader, Checkbox, Input, Loading, PageHeader, Select, Textarea } from '../../components/ui'
import { useToast } from '../../components/toast'
import { ProvinceCityFields, type ProvinceCity } from '../../components/ProvinceCityFields'
import { VenueModal } from '../../components/VenueModal'
import { EventWriter } from '../../components/EventWriter'
import { useFeatures } from '../../lib/ai'

const timezones = ['Africa/Johannesburg', 'Africa/Gaborone', 'Africa/Windhoek', 'Africa/Maputo', 'Africa/Lagos', 'Africa/Nairobi', 'Europe/London', 'UTC']

interface FormState {
  organiser_id: string
  category_id: string
  venue_id: string
  title: string
  summary: string
  description: string
  image_url: string
  starts_at: string
  ends_at: string
  timezone: string
  capacity: string
  allow_reentry: boolean
  refunds_allowed: boolean
  refund_cutoff_hours: string
}

const empty: FormState = {
  organiser_id: '',
  category_id: '',
  venue_id: '',
  title: '',
  summary: '',
  description: '',
  image_url: '',
  starts_at: '',
  ends_at: '',
  timezone: 'Africa/Johannesburg',
  capacity: '',
  allow_reentry: false,
  refunds_allowed: true,
  refund_cutoff_hours: '48',
}

export default function EventFormPage() {
  const { eventId } = useParams()
  const editing = !!eventId
  const { me, canFor } = useAuth()
  const navigate = useNavigate()
  const toast = useToast()
  const queryClient = useQueryClient()
  const fileInput = useRef<HTMLInputElement>(null)
  const [form, setForm] = useState<FormState>(empty)
  // Province and city narrow the venue list; the event itself stores only the venue.
  const [place, setPlace] = useState<ProvinceCity>({ province: '', city: '' })
  const [addingVenue, setAddingVenue] = useState(false)
  const features = useFeatures()
  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => setForm((f) => ({ ...f, [key]: value }))

  const existing = useQuery({ queryKey: ['admin', 'event', eventId], queryFn: () => api<AdminEvent>(`/admin/events/${eventId}`), enabled: editing })
  const categories = useQuery({ queryKey: ['public', 'categories'], queryFn: () => api<Category[]>('/public/categories') })
  const platformOrganisers = useQuery({
    queryKey: ['admin', 'organisers', 'active'],
    queryFn: () => apiPage<Organiser>('/admin/organisers', { status: 'active', per_page: 100 }),
    enabled: !!me?.all_organisers,
  })
  const venues = useQuery({
    queryKey: ['admin', 'venues', form.organiser_id],
    queryFn: () => api<Venue[]>('/admin/venues', { query: { organiser_id: form.organiser_id } }),
    enabled: !!form.organiser_id,
  })

  const venueList = useMemo(() => venues.data ?? [], [venues.data])
  const venueCities = useMemo(() => venueList.map((v) => ({ province: v.province, city: v.city })), [venueList])
  const sameCity = (a: string, b: string) => a.localeCompare(b, 'en-ZA', { sensitivity: 'base' }) === 0
  const venuesHere = venueList.filter((v) => v.province === place.province && sameCity(v.city, place.city))
  const organiserID = Number(form.organiser_id)
  const canAddVenue = organiserID > 0 && canFor(organiserID, P.VenuesManage)

  // Organisers this user may create events for.
  const organiserOptions = useMemo(() => {
    if (me?.all_organisers) return (platformOrganisers.data?.data ?? []).map((o) => ({ value: o.id, label: o.name }))
    return (me?.organisers ?? []).filter((o) => o.status === 'active' && canFor(o.id, P.EventsCreate)).map((o) => ({ value: o.id, label: o.name }))
  }, [me, platformOrganisers.data, canFor])

  useEffect(() => {
    const e = existing.data
    if (e) {
      setForm({
        organiser_id: String(e.organiser_id),
        category_id: e.category_id ? String(e.category_id) : '',
        venue_id: e.venue ? String(e.venue.id) : '',
        title: e.title,
        summary: e.summary,
        description: e.description,
        image_url: e.image_url,
        starts_at: e.starts_at_local,
        ends_at: e.ends_at_local,
        timezone: e.timezone,
        capacity: String(e.capacity),
        allow_reentry: e.allow_reentry,
        refunds_allowed: e.refunds_allowed,
        refund_cutoff_hours: String(e.refund_cutoff_hours),
      })
      if (e.venue) setPlace({ province: e.venue.province, city: e.venue.city })
    }
  }, [existing.data])

  // Preselect the organiser when there is only one choice.
  useEffect(() => {
    if (!editing && !form.organiser_id && organiserOptions.length === 1) set('organiser_id', String(organiserOptions[0].value))
  }, [editing, form.organiser_id, organiserOptions])

  const upload = useMutation({
    mutationFn: (file: File) => {
      const body = new FormData()
      body.append('file', file)
      return api<{ url: string }>('/admin/uploads', { method: 'POST', body })
    },
    onSuccess: (res) => set('image_url', res.url),
    onError: (e) => toast.error(errorMessage(e)),
  })

  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...form,
        organiser_id: Number(form.organiser_id) || 0,
        category_id: Number(form.category_id) || 0,
        venue_id: Number(form.venue_id) || 0,
        capacity: Number(form.capacity) || 0,
        refund_cutoff_hours: Number(form.refund_cutoff_hours) || 0,
      }
      return editing ? api<AdminEvent>(`/admin/events/${eventId}`, { method: 'PUT', body }) : api<AdminEvent>('/admin/events', { method: 'POST', body })
    },
    onSuccess: (e) => {
      queryClient.invalidateQueries({ queryKey: ['admin'] })
      toast.success(editing ? 'Event saved.' : 'Event created. Add ticket types next.')
      navigate(`/admin/events/${e.id}${editing ? '' : '?tab=tickets'}`)
    },
  })
  const errors = fieldErrors(save.error)

  if (editing && existing.isLoading) return <Loading />

  return (
    <>
      <PageHeader
        back={editing ? { to: `/admin/events/${eventId}`, label: 'Back to event' } : { to: '/admin/events', label: 'Events' }}
        title={editing ? `Edit ${existing.data?.title ?? 'event'}` : 'New event'}
        description={editing && existing.data?.status === 'published' ? 'Changes to a published event are visible to buyers immediately.' : 'Events start as drafts. Submit them for approval when they are ready.'}
      />
      <form
        onSubmit={(e) => {
          e.preventDefault()
          save.mutate()
        }}
        className="grid gap-6 xl:grid-cols-[1fr_360px]"
      >
        <div className="space-y-6">
          {save.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(save.error)}</Alert>}
          <Card>
            <CardHeader
              title="Details"
              actions={
                features.ai_event_writer && (
                  <EventWriter
                    organiserId={form.organiser_id}
                    currentTitle={form.title}
                    categories={categories.data ?? []}
                    hasText={!!(form.summary || form.description)}
                    onUse={(draft, categoryId) => {
                      setForm((f) => ({
                        ...f,
                        title: draft.title,
                        summary: draft.summary,
                        description: draft.description,
                        category_id: categoryId || f.category_id,
                      }))
                      toast.success('Text added to the form. Check it, then save.')
                    }}
                  />
                )
              }
            />
            <div className="space-y-4 p-5">
              {!editing && (
                <Select
                  label="Organiser"
                  options={organiserOptions}
                  placeholder="Choose an organiser"
                  value={form.organiser_id}
                  onChange={(e) => {
                    setForm((f) => ({ ...f, organiser_id: e.target.value, venue_id: '' }))
                    setPlace({ province: '', city: '' })
                  }}
                  error={errors.organiser_id}
                  required
                />
              )}
              <Input label="Title" value={form.title} onChange={(e) => set('title', e.target.value)} error={errors.title} required />
              <Input label="Summary" value={form.summary} onChange={(e) => set('summary', e.target.value)} error={errors.summary} hint="One line shown on event cards." maxLength={300} />
              <Textarea label="Description" rows={8} value={form.description} onChange={(e) => set('description', e.target.value)} error={errors.description} required />
              <Select
                label="Category"
                options={(categories.data ?? []).map((c) => ({ value: c.id, label: c.name }))}
                placeholder="Choose a category"
                value={form.category_id}
                onChange={(e) => set('category_id', e.target.value)}
                error={errors.category_id}
              />
            </div>
          </Card>

          <Card>
            <CardHeader title="Date and venue" />
            <div className="grid gap-4 p-5 sm:grid-cols-2">
              <Input label="Starts" type="datetime-local" value={form.starts_at} onChange={(e) => set('starts_at', e.target.value)} error={errors.starts_at} required />
              <Input label="Ends" type="datetime-local" value={form.ends_at} onChange={(e) => set('ends_at', e.target.value)} error={errors.ends_at} required />
              <Select label="Time zone" options={timezones.map((t) => ({ value: t, label: t.replace('_', ' ') }))} value={form.timezone} onChange={(e) => set('timezone', e.target.value)} error={errors.timezone} />
              <Input label="Capacity" type="number" min={1} value={form.capacity} onChange={(e) => set('capacity', e.target.value)} error={errors.capacity} hint="The most tickets that can be sold across all ticket types." required />
              <ProvinceCityFields
                value={place}
                onChange={(next) => {
                  setPlace(next)
                  set('venue_id', '')
                }}
                extraCities={venueCities}
                disabled={!form.organiser_id}
              />
              <Select
                label="Venue"
                className="sm:col-span-2"
                options={venuesHere.map((v) => ({ value: v.id, label: `${v.name}${v.address_line ? `, ${v.address_line}` : ''}${v.organiser_id ? '' : ' (shared)'}` }))}
                placeholder={
                  !form.organiser_id ? 'Choose an organiser first'
                    : !place.city ? 'Choose a province and city first'
                    : venuesHere.length ? 'Choose a venue' : `No venues in ${place.city} yet`
                }
                value={form.venue_id}
                onChange={(e) => set('venue_id', e.target.value)}
                error={errors.venue_id}
                disabled={!place.city || venuesHere.length === 0}
                hint={place.city && !venuesHere.length && !canAddVenue ? 'Ask an administrator to add a venue in this city.' : undefined}
              />
              {place.city && canAddVenue && (
                <div className="sm:col-span-2">
                  <Button type="button" variant="secondary" size="sm" icon={<Plus className="size-4" />} onClick={() => setAddingVenue(true)}>
                    Add a venue in {place.city}
                  </Button>
                </div>
              )}
            </div>
          </Card>
        </div>

        <div className="space-y-6">
          <Card>
            <CardHeader title="Image" description="JPEG, PNG or WebP up to 5 MB. Wide images work best." />
            <div className="p-5">
              {form.image_url ? (
                <div className="space-y-3">
                  <img src={form.image_url} alt="" className="aspect-video w-full rounded-lg object-cover" />
                  <Button type="button" variant="secondary" size="sm" icon={<Trash2 className="size-4" />} onClick={() => set('image_url', '')}>Remove image</Button>
                </div>
              ) : (
                <button
                  type="button"
                  onClick={() => fileInput.current?.click()}
                  className="flex aspect-video w-full flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-zinc-300 text-sm text-zinc-500 hover:border-brand-400 hover:text-brand-700"
                >
                  <ImagePlus className="size-6" />
                  {upload.isPending ? 'Uploading…' : 'Upload an image'}
                </button>
              )}
              <input
                ref={fileInput}
                type="file"
                accept="image/jpeg,image/png,image/webp,image/gif"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0]
                  if (file) upload.mutate(file)
                  e.target.value = ''
                }}
              />
              {errors.image_url && <p className="mt-2 text-sm text-red-600">{errors.image_url}</p>}
            </div>
          </Card>

          <Card>
            <CardHeader title="Entry and refunds" />
            <div className="space-y-4 p-5">
              <Checkbox label="Allow re-entry" description="Attendees can check out and back in again." checked={form.allow_reentry} onChange={(v) => set('allow_reentry', v)} />
              <Checkbox label="Allow refund requests" description="Buyers can ask for a refund before the cut-off." checked={form.refunds_allowed} onChange={(v) => set('refunds_allowed', v)} />
              {form.refunds_allowed && (
                <Input label="Refund cut-off (hours before start)" type="number" min={0} value={form.refund_cutoff_hours} onChange={(e) => set('refund_cutoff_hours', e.target.value)} error={errors.refund_cutoff_hours} />
              )}
            </div>
          </Card>

          <Button type="submit" size="lg" className="w-full" loading={save.isPending}>
            {editing ? 'Save changes' : 'Create draft event'}
          </Button>
        </div>
      </form>
      {addingVenue && (
        <VenueModal
          venue={null}
          organisers={organiserOptions}
          allowShared={false}
          initial={{ organiser_id: form.organiser_id, province: place.province, city: place.city }}
          onSaved={(v) => {
            // The server may tidy the city ("durban" becomes Durban), so follow what it saved.
            setPlace({ province: v.province, city: v.city })
            set('venue_id', String(v.id))
          }}
          onClose={() => setAddingVenue(false)}
        />
      )}
    </>
  )
}
