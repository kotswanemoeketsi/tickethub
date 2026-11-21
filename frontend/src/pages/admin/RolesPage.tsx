import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Lock, Plus } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { humanize } from '../../lib/format'
import type { Permission, Role } from '../../lib/types'
import { Alert, Badge, Button, Card, CardHeader, Checkbox, Input, Loading, Modal, PageHeader, Select, Tabs, Textarea } from '../../components/ui'
import { useToast } from '../../components/toast'

function RoleModal({ role, scope, permissions, onClose }: { role: Role | null; scope: Role['scope']; permissions: Permission[]; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [form, setForm] = useState({ name: role?.name ?? '', description: role?.description ?? '', scope: role?.scope ?? scope })
  const [granted, setGranted] = useState<Set<string>>(() => new Set(role?.permissions ?? []))
  const locked = !!role?.is_system

  // Organiser roles can only hold permissions that make sense inside one organiser.
  const available = permissions.filter((p) => form.scope === 'platform' || p.organiser)
  const modules = useMemo(() => {
    const map = new Map<string, Permission[]>()
    for (const p of available) map.set(p.module, [...(map.get(p.module) ?? []), p])
    return [...map.entries()]
  }, [available])

  const save = useMutation({
    mutationFn: () => {
      const body = { ...form, permissions: [...granted].filter((code) => available.some((p) => p.code === code)) }
      return role ? api(`/admin/roles/${role.id}`, { method: 'PUT', body }) : api('/admin/roles', { method: 'POST', body })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'roles'] })
      toast.success('Role saved. Members get the new permissions immediately.')
      onClose()
    },
  })
  const remove = useMutation({
    mutationFn: () => api(`/admin/roles/${role!.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'roles'] })
      toast.success('Role deleted.')
      onClose()
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const errors = fieldErrors(save.error)
  const toggle = (code: string, on: boolean) =>
    setGranted((s) => {
      const next = new Set(s)
      if (on) next.add(code)
      else next.delete(code)
      return next
    })

  return (
    <Modal
      open
      onClose={onClose}
      size="xl"
      title={role ? role.name : 'New role'}
      description={locked ? 'Built-in roles cannot be changed. Create a custom role to tailor permissions.' : 'Choose exactly what people with this role can do.'}
      footer={
        locked ? (
          <Button variant="secondary" onClick={onClose}>Close</Button>
        ) : (
          <>
            {role && (
              <Button variant="danger" className="mr-auto" loading={remove.isPending} disabled={role.user_count > 0} title={role.user_count > 0 ? 'Remove the role from everyone first' : undefined} onClick={() => window.confirm(`Delete ${role.name}?`) && remove.mutate()}>
                Delete role
              </Button>
            )}
            <Button variant="secondary" onClick={onClose}>Cancel</Button>
            <Button loading={save.isPending} onClick={() => save.mutate()}>Save role</Button>
          </>
        )
      }
    >
      <div className="space-y-5">
        {save.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(save.error)}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} error={errors.name} disabled={locked} placeholder="e.g. Marketing Manager" />
          <Select
            label="Applies to"
            value={form.scope}
            onChange={(e) => setForm({ ...form, scope: e.target.value as Role['scope'] })}
            disabled={!!role}
            options={[{ value: 'platform', label: 'The whole platform (staff)' }, { value: 'organiser', label: "An organiser's team" }]}
            error={errors.scope}
          />
          <Textarea label="Description" className="sm:col-span-2" rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} disabled={locked} />
        </div>
        {errors.permissions && <Alert tone="error">{errors.permissions}</Alert>}
        <div className="grid gap-4 md:grid-cols-2">
          {modules.map(([module, perms]) => (
            <div key={module} className="rounded-xl border border-zinc-200 p-4">
              <div className="mb-3 flex items-center justify-between">
                <p className="font-medium text-zinc-900">{humanize(module)}</p>
                {!locked && (
                  <button
                    type="button"
                    className="text-xs text-brand-700"
                    onClick={() => {
                      const all = perms.every((p) => granted.has(p.code))
                      perms.forEach((p) => toggle(p.code, !all))
                    }}
                  >
                    {perms.every((p) => granted.has(p.code)) ? 'Clear' : 'Select all'}
                  </button>
                )}
              </div>
              <div className="space-y-2.5">
                {perms.map((p) => (
                  <Checkbox key={p.code} label={p.description} description={p.code} checked={granted.has(p.code)} disabled={locked} onChange={(on) => toggle(p.code, on)} />
                ))}
              </div>
            </div>
          ))}
        </div>
      </div>
    </Modal>
  )
}

export default function RolesPage() {
  const [scope, setScope] = useState<Role['scope']>('platform')
  const [editing, setEditing] = useState<Role | null | undefined>(undefined)
  const roles = useQuery({ queryKey: ['admin', 'roles', 'all'], queryFn: () => api<Role[]>('/admin/roles') })
  const permissions = useQuery({ queryKey: ['admin', 'permissions'], queryFn: () => api<Permission[]>('/admin/permissions') })
  if (roles.isLoading || permissions.isLoading) return <Loading />
  const list = (roles.data ?? []).filter((r) => r.scope === scope)

  return (
    <>
      <PageHeader
        title="Roles & permissions"
        description="Built-in roles cover common jobs. Create custom roles, such as a Marketing Manager, for anything else."
        actions={<Button icon={<Plus className="size-4" />} onClick={() => setEditing(null)}>New role</Button>}
      />
      <Tabs value={scope} onChange={setScope} tabs={[{ value: 'platform', label: 'Platform roles' }, { value: 'organiser', label: 'Organiser team roles' }]} />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {list.map((r) => (
          <button key={r.id} onClick={() => setEditing(r)} className="text-left">
            <Card className="h-full transition hover:border-brand-300 hover:shadow-sm">
              <CardHeader
                title={<span className="flex items-center gap-2">{r.name} {r.is_system ? <Lock className="size-3.5 text-zinc-400" /> : <Badge tone="indigo">Custom</Badge>}</span>}
                description={r.description}
              />
              <p className="px-5 pb-4 text-sm text-zinc-500">{r.permissions.length} permissions, {r.user_count} {r.user_count === 1 ? 'person' : 'people'}</p>
            </Card>
          </button>
        ))}
      </div>
      {editing !== undefined && <RoleModal role={editing} scope={scope} permissions={permissions.data ?? []} onClose={() => setEditing(undefined)} />}
    </>
  )
}
