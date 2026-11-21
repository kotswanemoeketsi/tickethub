import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { P } from '../../lib/permissions'
import type { Category, Venue } from '../../lib/types'
import { Alert, Badge, Button, Card, CardHeader, Input, Modal, PageHeader, Tabs, Textarea } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'
import { VenueModal } from '../../components/VenueModal'

function useDelete(path: string, queryKey: string[], label: string) {
  const toast = useToast()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api(`${path}/${id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey })
      toast.success(`${label} deleted.`)
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
}

function CategoryModal({ category, onClose }: { category: Category | null; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ name: category?.name ?? '', description: category?.description ?? '' })
  const save = useMutation({
    mutationFn: () => (category ? api(`/admin/categories/${category.id}`, { method: 'PUT', body: form }) : api('/admin/categories', { method: 'POST', body: form })),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['public', 'categories'] })
      toast.success('Category saved.')
      onClose()
    },
  })
  const errors = fieldErrors(save.error)
  return (
    <Modal open onClose={onClose} title={category ? 'Edit category' : 'New category'} footer={<><Button variant="secondary" onClick={onClose}>Cancel</Button><Button loading={save.isPending} onClick={() => save.mutate()}>Save</Button></>}>
      <div className="space-y-4">
        {save.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(save.error)}</Alert>}
        <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} error={errors.name} />
        <Textarea label="Description" rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} error={errors.description} />
      </div>
    </Modal>
  )
}

export default function CataloguePage() {
  const { me, can, canPlatform, canFor } = useAuth()
  const canCategories = canPlatform(P.CategoriesManage)
  const canVenues = can(P.VenuesManage)
  const [tab, setTab] = useState<'categories' | 'venues'>(canCategories ? 'categories' : 'venues')
  const [category, setCategory] = useState<Category | null | undefined>(undefined)
  const [venue, setVenue] = useState<Venue | null | undefined>(undefined)
  const categories = useQuery({ queryKey: ['public', 'categories'], queryFn: () => api<Category[]>('/public/categories'), enabled: canCategories })
  const venues = useQuery({ queryKey: ['admin', 'venues', 'all'], queryFn: () => api<Venue[]>('/admin/venues'), enabled: canVenues })
  const deleteCategory = useDelete('/admin/categories', ['public', 'categories'], 'Category')
  const deleteVenue = useDelete('/admin/venues', ['admin', 'venues'], 'Venue')

  const sharedVenues = canPlatform(P.VenuesManage)
  const organisers = (me?.organisers ?? []).filter((o) => canFor(o.id, P.VenuesManage)).map((o) => ({ value: o.id, label: o.name }))
  const organiserName = (id: number | null) => (id === null ? null : me?.organisers.find((o) => o.id === id)?.name ?? `Organiser #${id}`)
  const editableVenue = (v: Venue) => (v.organiser_id === null ? sharedVenues : canFor(v.organiser_id, P.VenuesManage))

  const categoryColumns: Column<Category>[] = [
    { header: 'Name', cell: (c) => <div><p className="font-medium text-zinc-900">{c.name}</p><p className="text-xs text-zinc-500">{c.description}</p></div> },
    { header: 'Live events', cell: (c) => c.event_count, align: 'right' },
    {
      header: '',
      align: 'right',
      cell: (c) => (
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="ghost" onClick={() => setCategory(c)} aria-label={`Edit ${c.name}`}><Pencil className="size-4" /></Button>
          <Button size="sm" variant="ghost" onClick={() => window.confirm(`Delete ${c.name}?`) && deleteCategory.mutate(c.id)} aria-label={`Delete ${c.name}`}><Trash2 className="size-4" /></Button>
        </div>
      ),
    },
  ]
  const venueColumns: Column<Venue>[] = [
    { header: 'Venue', cell: (v) => <div><p className="font-medium text-zinc-900">{v.name}</p><p className="text-xs text-zinc-500">{[v.address_line, v.city, v.province].filter(Boolean).join(', ')}</p></div> },
    { header: 'Owner', cell: (v) => (v.organiser_id === null ? <Badge tone="blue">Shared</Badge> : organiserName(v.organiser_id)) },
    { header: 'Capacity', cell: (v) => v.capacity?.toLocaleString() ?? '—', align: 'right' },
    {
      header: '',
      align: 'right',
      cell: (v) =>
        editableVenue(v) && (
          <div className="flex justify-end gap-1">
            <Button size="sm" variant="ghost" onClick={() => setVenue(v)} aria-label={`Edit ${v.name}`}><Pencil className="size-4" /></Button>
            <Button size="sm" variant="ghost" onClick={() => window.confirm(`Delete ${v.name}?`) && deleteVenue.mutate(v.id)} aria-label={`Delete ${v.name}`}><Trash2 className="size-4" /></Button>
          </div>
        ),
    },
  ]

  return (
    <>
      <PageHeader title="Categories & venues" />
      <Tabs
        value={tab}
        onChange={setTab}
        tabs={[...(canCategories ? [{ value: 'categories' as const, label: 'Categories' }] : []), ...(canVenues ? [{ value: 'venues' as const, label: 'Venues' }] : [])]}
      />
      {tab === 'categories' && canCategories && (
        <Card>
          <CardHeader title="Event categories" actions={<Button size="sm" icon={<Plus className="size-4" />} onClick={() => setCategory(null)}>New category</Button>} />
          <DataTable columns={categoryColumns} rows={categories.data} loading={categories.isLoading} rowKey={(c) => c.id} />
        </Card>
      )}
      {tab === 'venues' && canVenues && (
        <Card>
          <CardHeader title="Venues" description="Shared venues are available to every organiser." actions={<Button size="sm" icon={<Plus className="size-4" />} onClick={() => setVenue(null)}>New venue</Button>} />
          <DataTable columns={venueColumns} rows={venues.data} loading={venues.isLoading} rowKey={(v) => v.id} empty={{ title: 'No venues yet' }} />
        </Card>
      )}
      {category !== undefined && <CategoryModal category={category} onClose={() => setCategory(undefined)} />}
      {venue !== undefined && <VenueModal venue={venue} organisers={organisers} allowShared={sharedVenues} onClose={() => setVenue(undefined)} />}
    </>
  )
}
