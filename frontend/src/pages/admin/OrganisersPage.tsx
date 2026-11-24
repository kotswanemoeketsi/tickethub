import { useState } from 'react'
import { useNavigate } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDate } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { Organiser } from '../../lib/types'
import { Alert, Button, Card, Input, Modal, PageHeader, Pagination, Select, StatusBadge, Textarea } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'

function CreateOrganiserModal({ onClose }: { onClose: () => void }) {
  const toast = useToast()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ name: '', email: '', phone: '', description: '', commission: '3.5', owner_email: '', owner_first_name: '', owner_last_name: '' })
  const create = useMutation({
    mutationFn: () => {
      const { commission, ...rest } = form
      return api<Organiser>('/admin/organisers', { method: 'POST', body: { ...rest, commission_bps: Math.round(Number(commission) * 100) } })
    },
    onSuccess: (o) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'organisers'] })
      toast.success('Organiser created. The owner has been invited by email.')
      navigate(`/admin/organisers/${o.id}`)
    },
  })
  const errors = fieldErrors(create.error)
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }))
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title="New organiser"
      description="Organisers created here are active straight away."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={create.isPending} onClick={() => create.mutate()}>Create organiser</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {create.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(create.error)}</Alert></div>}
        <Input label="Name" value={form.name} onChange={set('name')} error={errors.name} />
        <Input label="Commission (%)" hint="Plus R5 per paid ticket, deducted from organiser proceeds. Free tickets are exempt." inputMode="decimal" value={form.commission} onChange={set('commission')} error={errors.commission_bps} />
        <Input label="Contact email" type="email" value={form.email} onChange={set('email')} error={errors.email} />
        <Input label="Phone" value={form.phone} onChange={set('phone')} error={errors.phone} />
        <Textarea label="Description" className="sm:col-span-2" rows={2} value={form.description} onChange={set('description')} error={errors.description} />
        <p className="text-sm font-medium text-zinc-700 sm:col-span-2">Owner account</p>
        <Input label="First name" value={form.owner_first_name} onChange={set('owner_first_name')} error={errors.owner_first_name} />
        <Input label="Last name" value={form.owner_last_name} onChange={set('owner_last_name')} error={errors.owner_last_name} />
        <Input label="Email" type="email" className="sm:col-span-2" value={form.owner_email} onChange={set('owner_email')} error={errors.owner_email} hint="A new account is invited by email; an existing user is added as owner." />
      </div>
    </Modal>
  )
}

export default function OrganisersPage() {
  const { canPlatform } = useAuth()
  const navigate = useNavigate()
  const [q, setQ] = useQueryParam('q')
  const [status, setStatus] = useQueryParam('status')
  const [page, setPage] = useQueryParam('page', '1')
  const [creating, setCreating] = useState(false)
  const query = useDebounced(q, 300)
  const list = useQuery({ queryKey: ['admin', 'organisers', { query, status, page }], queryFn: () => apiPage<Organiser>('/admin/organisers', { q: query, status, page }) })

  const columns: Column<Organiser>[] = [
    { header: 'Organiser', cell: (o) => <div><p className="font-medium text-zinc-900">{o.name}</p><p className="text-xs text-zinc-500">{o.email}</p></div> },
    { header: 'Events', cell: (o) => o.event_count, align: 'right' },
    { header: 'Team', cell: (o) => o.member_count, align: 'right' },
    { header: 'Commission', cell: (o) => `${o.commission_bps / 100}% + R5 / paid ticket`, align: 'right' },
    { header: 'Status', cell: (o) => <StatusBadge status={o.status} /> },
    { header: 'Joined', cell: (o) => formatDate(o.created_at) },
  ]
  return (
    <>
      <PageHeader
        title="Organisers"
        description="Businesses selling tickets on the platform. New applications wait here for approval."
        actions={canPlatform(P.OrganisersManage) && <Button icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>New organiser</Button>}
      />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Search by name or email" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search organisers" />
          <Select aria-label="Status" className="w-44" placeholder="All statuses" value={status} onChange={(e) => setStatus(e.target.value)} options={[{ value: 'pending', label: 'Pending approval' }, { value: 'active', label: 'Active' }, { value: 'suspended', label: 'Suspended' }]} />
        </div>
        <DataTable columns={columns} rows={list.data?.data} loading={list.isLoading} rowKey={(o) => o.id} onRowClick={(o) => navigate(`/admin/organisers/${o.id}`)} empty={{ title: 'No organisers found' }} />
        <Pagination meta={list.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
      {creating && <CreateOrganiserModal onClose={() => setCreating(false)} />}
    </>
  )
}
