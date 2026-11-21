import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { KeyRound, Plus } from 'lucide-react'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, fullName } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { Role, User } from '../../lib/types'
import { Alert, Badge, Button, Card, Checkbox, Input, Modal, PageHeader, Pagination, Select, StatusBadge } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'

const SUPER_ADMIN = 'super_admin'

/** Creates a user or edits one, including their platform roles. */
function UserModal({ user, roles, onClose }: { user: User | null; roles: Role[]; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const { me } = useAuth()
  const [form, setForm] = useState({
    first_name: user?.first_name ?? '',
    last_name: user?.last_name ?? '',
    email: user?.email ?? '',
    phone: user?.phone ?? '',
    password: '',
  })
  const [roleIds, setRoleIds] = useState<number[]>(user?.roles.map((r) => r.id) ?? [])

  const save = useMutation({
    mutationFn: async () => {
      if (!user) return api<User>('/admin/users', { method: 'POST', body: { ...form, role_ids: roleIds } })
      await api(`/admin/users/${user.id}`, { method: 'PUT', body: { first_name: form.first_name, last_name: form.last_name, email: form.email, phone: form.phone } })
      return api<User>(`/admin/users/${user.id}/roles`, { method: 'PUT', body: { role_ids: roleIds } })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
      toast.success(user ? 'User saved.' : form.password ? 'User created.' : 'User created and invited by email.')
      onClose()
    },
  })
  const status = useMutation({
    mutationFn: (active: boolean) => api(`/admin/users/${user!.id}/${active ? 'activate' : 'deactivate'}`, { method: 'POST' }),
    onSuccess: (_, active) => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'users'] })
      toast.success(active ? 'Account activated.' : 'Account deactivated and signed out everywhere.')
      onClose()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const reset = useMutation({
    mutationFn: () => api<{ message: string }>(`/admin/users/${user!.id}/password-reset`, { method: 'POST' }),
    onSuccess: (r) => toast.success(r.message),
    onError: (e) => toast.error(errorMessage(e)),
  })
  const errors = fieldErrors(save.error)
  const self = user?.id === me?.user.id
  // Only a super administrator may grant the Super Administrator role or
  // change a super administrator's account; the API enforces the same rule.
  const superAdmin = !!me?.is_super_admin
  const locked = !superAdmin && !!user?.roles.some((r) => r.slug === SUPER_ADMIN)
  const grantable = roles.filter((r) => r.slug !== SUPER_ADMIN || superAdmin)

  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={user ? fullName(user.first_name, user.last_name) : 'New user'}
      description={user ? `Last signed in ${formatDateTime(user.last_login_at)}` : 'Leave the password empty to email an invitation instead.'}
      footer={
        <>
          {user && !self && !locked && (
            <Button variant={user.status === 'active' ? 'danger' : 'success'} className="mr-auto" loading={status.isPending} onClick={() => status.mutate(user.status !== 'active')}>
              {user.status === 'active' ? 'Deactivate' : 'Activate'}
            </Button>
          )}
          {user && !locked && <Button variant="secondary" icon={<KeyRound className="size-4" />} loading={reset.isPending} onClick={() => reset.mutate()}>Send password reset</Button>}
          <Button variant="secondary" onClick={onClose}>{locked ? 'Close' : 'Cancel'}</Button>
          {!locked && <Button loading={save.isPending} onClick={() => save.mutate()}>Save</Button>}
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {locked && <div className="sm:col-span-2"><Alert tone="info">Only a super administrator can change a super administrator's account.</Alert></div>}
        {save.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(save.error)}</Alert></div>}
        <Input label="First name" disabled={locked} value={form.first_name} onChange={(e) => setForm({ ...form, first_name: e.target.value })} error={errors.first_name} />
        <Input label="Last name" disabled={locked} value={form.last_name} onChange={(e) => setForm({ ...form, last_name: e.target.value })} error={errors.last_name} />
        <Input label="Email" type="email" disabled={locked} value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} />
        <Input label="Phone" disabled={locked} value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} error={errors.phone} />
        {!user && <Input label="Password (optional)" type="password" autoComplete="new-password" className="sm:col-span-2" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} error={errors.password} />}
        <div className="space-y-3 sm:col-span-2">
          <p className="text-sm font-medium text-zinc-700">Platform roles</p>
          {errors.role_ids && <p className="text-sm text-red-600">{errors.role_ids}</p>}
          {(locked ? roles.filter((r) => roleIds.includes(r.id)) : grantable).map((r) => (
            <Checkbox
              key={r.id}
              disabled={locked}
              label={r.name}
              description={r.description}
              checked={roleIds.includes(r.id)}
              onChange={(on) => setRoleIds((ids) => (on ? [...ids, r.id] : ids.filter((id) => id !== r.id)))}
            />
          ))}
        </div>
        {user && user.memberships.length > 0 && (
          <div className="sm:col-span-2">
            <p className="mb-2 text-sm font-medium text-zinc-700">Organiser teams</p>
            <ul className="space-y-1 text-sm text-zinc-600">
              {user.memberships.map((m) => <li key={m.organiser_id}>{m.organiser_name} — {m.role_name}</li>)}
            </ul>
            <p className="mt-1 text-xs text-zinc-500">Team roles are managed on each organiser's page.</p>
          </div>
        )}
      </div>
    </Modal>
  )
}

export default function UsersPage() {
  const { canPlatform } = useAuth()
  const [q, setQ] = useQueryParam('q')
  const [status, setStatus] = useQueryParam('status')
  const [roleId, setRoleId] = useQueryParam('role')
  const [page, setPage] = useQueryParam('page', '1')
  const [editing, setEditing] = useState<User | null | undefined>(undefined)
  const query = useDebounced(q, 300)
  const canManage = canPlatform(P.UsersManage)
  const users = useQuery({ queryKey: ['admin', 'users', { query, status, roleId, page }], queryFn: () => apiPage<User>('/admin/users', { q: query, status, role_id: roleId, page }) })
  const roles = useQuery({ queryKey: ['admin', 'roles', 'platform'], queryFn: () => api<Role[]>('/admin/roles', { query: { scope: 'platform' } }) })

  const columns: Column<User>[] = [
    { header: 'User', cell: (u) => <div><p className="font-medium text-zinc-900">{fullName(u.first_name, u.last_name)}</p><p className="text-xs text-zinc-500">{u.email}</p></div> },
    {
      header: 'Roles',
      cell: (u) => (
        <div className="flex flex-wrap gap-1">
          {u.roles.map((r) => <Badge key={r.id} tone="indigo">{r.name}</Badge>)}
          {u.memberships.map((m) => <Badge key={`m${m.organiser_id}`}>{m.role_name}, {m.organiser_name}</Badge>)}
          {u.roles.length === 0 && u.memberships.length === 0 && <span className="text-xs text-zinc-400">Customer</span>}
        </div>
      ),
    },
    { header: 'Status', cell: (u) => <StatusBadge status={u.status} /> },
    { header: 'Last sign-in', cell: (u) => formatDateTime(u.last_login_at) },
  ]

  return (
    <>
      <PageHeader title="Users" description="Everyone with an account: staff, organiser teams and customers." actions={canManage && <Button icon={<Plus className="size-4" />} onClick={() => setEditing(null)}>New user</Button>} />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Search by name or email" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search users" />
          <Select aria-label="Role" className="w-48" placeholder="All roles" value={roleId} onChange={(e) => setRoleId(e.target.value)} options={(roles.data ?? []).map((r) => ({ value: r.id, label: r.name }))} />
          <Select aria-label="Status" className="w-40" placeholder="All statuses" value={status} onChange={(e) => setStatus(e.target.value)} options={[{ value: 'active', label: 'Active' }, { value: 'inactive', label: 'Inactive' }]} />
        </div>
        <DataTable columns={columns} rows={users.data?.data} loading={users.isLoading} rowKey={(u) => u.id} onRowClick={canManage ? (u) => setEditing(u) : undefined} empty={{ title: 'No users found' }} />
        <Pagination meta={users.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
      {editing !== undefined && <UserModal user={editing} roles={roles.data ?? []} onClose={() => setEditing(undefined)} />}
    </>
  )
}
