import { useEffect, useState } from 'react'
import { useParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Trash2, UserPlus } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDate, fullName } from '../../lib/format'
import { P } from '../../lib/permissions'
import type { Member, Organiser, Role } from '../../lib/types'
import { Alert, Button, Card, CardHeader, EmptyState, Input, Loading, Modal, PageHeader, Select, StatusBadge, Textarea } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'
import { Forbidden } from '../../layouts/guards'

function ProfileCard({ organiser, platformAdmin }: { organiser: Organiser; platformAdmin: boolean }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ name: '', email: '', phone: '', description: '', commission: '' })
  useEffect(() => {
    setForm({ name: organiser.name, email: organiser.email, phone: organiser.phone, description: organiser.description, commission: String(organiser.commission_bps / 100) })
  }, [organiser])
  const save = useMutation({
    mutationFn: () =>
      api(`/admin/organisers/${organiser.id}`, {
        method: 'PUT',
        body: {
          name: form.name,
          email: form.email,
          phone: form.phone,
          description: form.description,
          // Only platform administrators may change commission; others leave it untouched.
          commission_bps: platformAdmin ? Math.round(Number(form.commission) * 100) : undefined,
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'organiser', String(organiser.id)] })
      toast.success('Organiser saved.')
    },
  })
  const errors = fieldErrors(save.error)
  return (
    <Card>
      <CardHeader title="Profile" />
      <form className="grid gap-4 p-5 sm:grid-cols-2" onSubmit={(e) => { e.preventDefault(); save.mutate() }}>
        {save.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(save.error)}</Alert></div>}
        <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} error={errors.name} />
        <Input label="Contact email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} />
        <Input label="Phone" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} error={errors.phone} />
        <Input label="Commission (%)" hint="Plus R5 per paid ticket, deducted from organiser proceeds. Free tickets are exempt." inputMode="decimal" value={form.commission} onChange={(e) => setForm({ ...form, commission: e.target.value })} error={errors.commission_bps} disabled={!platformAdmin} />
        <Textarea label="Description" className="sm:col-span-2" rows={3} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} error={errors.description} />
        <div className="flex justify-end sm:col-span-2"><Button type="submit" loading={save.isPending}>Save</Button></div>
      </form>
    </Card>
  )
}

function AddMemberModal({ organiserId, roles, onClose }: { organiserId: number; roles: Role[]; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ email: '', first_name: '', last_name: '', role_id: '' })
  const add = useMutation({
    mutationFn: () => api(`/admin/organisers/${organiserId}/members`, { method: 'POST', body: { ...form, role_id: Number(form.role_id) || 0 } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'organiser', String(organiserId), 'members'] })
      toast.success('Team member added.')
      onClose()
    },
  })
  const errors = fieldErrors(add.error)
  return (
    <Modal
      open
      onClose={onClose}
      title="Add team member"
      description="New people receive an email invitation to set their password."
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={add.isPending} onClick={() => add.mutate()}>Add member</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {add.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(add.error)}</Alert></div>}
        <Input label="Email" type="email" className="sm:col-span-2" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} />
        <Input label="First name" value={form.first_name} onChange={(e) => setForm({ ...form, first_name: e.target.value })} error={errors.first_name} />
        <Input label="Last name" value={form.last_name} onChange={(e) => setForm({ ...form, last_name: e.target.value })} error={errors.last_name} />
        <Select
          label="Role"
          className="sm:col-span-2"
          placeholder="Choose a role"
          options={roles.map((r) => ({ value: r.id, label: r.name }))}
          value={form.role_id}
          onChange={(e) => setForm({ ...form, role_id: e.target.value })}
          error={errors.role_id}
          hint="You can only grant roles whose permissions you hold yourself."
        />
      </div>
    </Modal>
  )
}

export default function OrganiserDetailPage() {
  const { organiserId = '' } = useParams()
  const { canPlatform, canFor, me } = useAuth()
  const toast = useToast()
  const queryClient = useQueryClient()
  const [adding, setAdding] = useState(false)
  const id = Number(organiserId)
  const platformView = canPlatform(P.OrganisersView)
  const allowed = platformView || !!me?.organisers.some((o) => o.id === id)
  const canTeam = canFor(id, P.TeamManage)
  const canManagePlatform = canPlatform(P.OrganisersManage)

  const organiser = useQuery({ queryKey: ['admin', 'organiser', organiserId], queryFn: () => api<Organiser>(`/admin/organisers/${organiserId}`), enabled: allowed })
  const members = useQuery({ queryKey: ['admin', 'organiser', organiserId, 'members'], queryFn: () => api<Member[]>(`/admin/organisers/${organiserId}/members`), enabled: allowed })
  const roles = useQuery({ queryKey: ['admin', 'roles', 'organiser'], queryFn: () => api<Role[]>('/admin/roles', { query: { scope: 'organiser' } }), enabled: canTeam })

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['admin', 'organiser', organiserId] })
  const setStatus = useMutation({
    mutationFn: (action: 'approve' | 'suspend' | 'reactivate') => api(`/admin/organisers/${organiserId}/${action}`, { method: 'POST' }),
    onSuccess: () => {
      invalidate()
      queryClient.invalidateQueries({ queryKey: ['admin', 'organisers'] })
      toast.success('Status updated.')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const changeRole = useMutation({
    mutationFn: ({ userId, roleId }: { userId: number; roleId: number }) => api(`/admin/organisers/${organiserId}/members/${userId}`, { method: 'PUT', body: { role_id: roleId } }),
    onSuccess: () => {
      invalidate()
      toast.success('Role changed.')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const removeMember = useMutation({
    mutationFn: (userId: number) => api(`/admin/organisers/${organiserId}/members/${userId}`, { method: 'DELETE' }),
    onSuccess: () => {
      invalidate()
      toast.success('Team member removed.')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })

  if (!allowed) return <Forbidden />
  if (organiser.isLoading) return <Loading />
  if (!organiser.data) return <EmptyState title="Organiser not found" />
  const o = organiser.data
  const editable = canManagePlatform || canTeam

  const columns: Column<Member>[] = [
    { header: 'Name', cell: (m) => <div><p className="font-medium text-zinc-900">{fullName(m.first_name, m.last_name)}</p><p className="text-xs text-zinc-500">{m.email}</p></div> },
    {
      header: 'Role',
      cell: (m) =>
        canTeam && roles.data ? (
          <select
            className="rounded-md border-0 bg-transparent py-1 text-sm ring-1 ring-inset ring-zinc-200 focus:ring-2 focus:ring-brand-600"
            value={m.role_id}
            onChange={(e) => changeRole.mutate({ userId: m.user_id, roleId: Number(e.target.value) })}
            aria-label={`Role for ${m.email}`}
          >
            {roles.data.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
          </select>
        ) : (
          m.role_name
        ),
    },
    { header: 'Account', cell: (m) => <StatusBadge status={m.user_status} /> },
    { header: 'Joined', cell: (m) => formatDate(m.joined_at) },
    {
      header: '',
      align: 'right',
      cell: (m) =>
        canTeam && m.user_id !== me?.user.id ? (
          <Button size="sm" variant="ghost" onClick={() => window.confirm(`Remove ${m.email} from ${o.name}?`) && removeMember.mutate(m.user_id)} aria-label={`Remove ${m.email}`}>
            <Trash2 className="size-4" />
          </Button>
        ) : null,
    },
  ]

  return (
    <>
      <PageHeader
        back={platformView ? { to: '/admin/organisers', label: 'Organisers' } : undefined}
        title={<span className="flex items-center gap-3">{o.name} <StatusBadge status={o.status} /></span>}
        description={`${o.event_count} events, joined ${formatDate(o.created_at)}`}
        actions={
          canManagePlatform && (
            <>
              {o.status === 'pending' && <Button variant="success" loading={setStatus.isPending} onClick={() => setStatus.mutate('approve')}>Approve</Button>}
              {o.status === 'active' && <Button variant="danger" loading={setStatus.isPending} onClick={() => window.confirm('Suspend this organiser? Their events stop selling.') && setStatus.mutate('suspend')}>Suspend</Button>}
              {o.status === 'suspended' && <Button loading={setStatus.isPending} onClick={() => setStatus.mutate('reactivate')}>Reactivate</Button>}
            </>
          )
        }
      />
      {o.status === 'pending' && <div className="mb-4"><Alert tone="warning" title="Awaiting approval">This organiser cannot publish events until the application is approved.</Alert></div>}
      <div className="grid gap-6 xl:grid-cols-2">
        {editable ? <ProfileCard organiser={o} platformAdmin={canManagePlatform} /> : (
          <Card className="p-5 text-sm text-zinc-600">{o.description || 'No description.'}</Card>
        )}
        <Card>
          <CardHeader title="Team" description="People who manage this organiser's events." actions={canTeam && <Button size="sm" icon={<UserPlus className="size-4" />} onClick={() => setAdding(true)}>Add member</Button>} />
          <DataTable columns={columns} rows={members.data} loading={members.isLoading} rowKey={(m) => m.user_id} empty={{ title: 'No team members' }} />
        </Card>
      </div>
      {adding && <AddMemberModal organiserId={o.id} roles={roles.data ?? []} onClose={() => setAdding(false)} />}
    </>
  )
}
