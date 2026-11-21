import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../../lib/api'
import { formatMoney, fromCents, toCents } from '../../../lib/format'
import type { AdminEvent, Promotion, TicketType } from '../../../lib/types'
import { Alert, Badge, Button, Card, CardHeader, Checkbox, Input, Modal, Select, Textarea } from '../../../components/ui'
import { DataTable, type Column } from '../../../components/DataTable'
import { useToast } from '../../../components/toast'

function describe(p: Promotion, currency: string) {
  return p.discount_type === 'percentage' ? `${p.discount_value / 100}% off` : `${formatMoney(p.discount_value, currency)} off`
}

function PromotionModal({ event, promotion, ticketTypes, onClose }: { event: AdminEvent; promotion: Promotion | null; ticketTypes: TicketType[]; onClose: () => void }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [form, setForm] = useState(() => ({
    code: promotion?.code ?? '',
    description: promotion?.description ?? '',
    discount_type: promotion?.discount_type ?? 'percentage',
    // Percentages are typed as whole numbers (10 = 10%) and amounts in rand.
    value: promotion ? (promotion.discount_type === 'percentage' ? String(promotion.discount_value / 100) : fromCents(promotion.discount_value)) : '',
    max_uses: promotion?.max_uses != null ? String(promotion.max_uses) : '',
    starts_at: promotion?.starts_at_local ?? '',
    ends_at: promotion?.ends_at_local ?? '',
    is_active: promotion?.is_active ?? true,
    ticket_type_ids: promotion?.ticket_type_ids ?? [],
  }))
  const save = useMutation({
    mutationFn: () => {
      const body = {
        code: form.code,
        description: form.description,
        discount_type: form.discount_type,
        discount_value: form.discount_type === 'percentage' ? Math.round(Number(form.value) * 100) || 0 : toCents(form.value),
        max_uses: form.max_uses === '' ? null : Number(form.max_uses),
        starts_at: form.starts_at,
        ends_at: form.ends_at,
        is_active: form.is_active,
        ticket_type_ids: form.ticket_type_ids,
      }
      return promotion ? api(`/admin/promotions/${promotion.id}`, { method: 'PUT', body }) : api(`/admin/events/${event.id}/promotions`, { method: 'POST', body })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'event', String(event.id), 'promotions'] })
      toast.success('Promo code saved.')
      onClose()
    },
  })
  const errors = fieldErrors(save.error)
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={promotion ? `Edit ${promotion.code}` : 'New promo code'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>Save</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {save.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(save.error)}</Alert></div>}
        <Input label="Code" value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value.toUpperCase() })} error={errors.code} placeholder="EARLY20" />
        <Select
          label="Discount type"
          options={[{ value: 'percentage', label: 'Percentage' }, { value: 'fixed', label: `Fixed amount per ticket (${event.currency})` }]}
          value={form.discount_type}
          onChange={(e) => setForm({ ...form, discount_type: e.target.value as Promotion['discount_type'] })}
        />
        <Input label={form.discount_type === 'percentage' ? 'Percentage off' : 'Amount off per ticket'} inputMode="decimal" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} error={errors.discount_value} />
        <Input label="Maximum uses (optional)" type="number" min={1} value={form.max_uses} onChange={(e) => setForm({ ...form, max_uses: e.target.value })} error={errors.max_uses} hint="Each ticket counts as one use." />
        <Input label="Valid from (optional)" type="datetime-local" value={form.starts_at} onChange={(e) => setForm({ ...form, starts_at: e.target.value })} error={errors.starts_at} />
        <Input label="Valid until (optional)" type="datetime-local" value={form.ends_at} onChange={(e) => setForm({ ...form, ends_at: e.target.value })} error={errors.ends_at} />
        <Textarea label="Internal note" className="sm:col-span-2" rows={2} value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
        <div className="space-y-2 sm:col-span-2">
          <p className="text-sm font-medium text-zinc-700">Applies to</p>
          <p className="text-xs text-zinc-500">Leave all unticked to apply the code to every ticket type.</p>
          {ticketTypes.map((t) => (
            <Checkbox
              key={t.id}
              label={t.name}
              checked={form.ticket_type_ids.includes(t.id)}
              onChange={(on) => setForm((f) => ({ ...f, ticket_type_ids: on ? [...f.ticket_type_ids, t.id] : f.ticket_type_ids.filter((id) => id !== t.id) }))}
            />
          ))}
        </div>
        <Checkbox label="Active" checked={form.is_active} onChange={(v) => setForm({ ...form, is_active: v })} />
      </div>
    </Modal>
  )
}

export function PromotionsTab({ event }: { event: AdminEvent }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [editing, setEditing] = useState<Promotion | null | undefined>(undefined)
  const promos = useQuery({ queryKey: ['admin', 'event', String(event.id), 'promotions'], queryFn: () => api<Promotion[]>(`/admin/events/${event.id}/promotions`) })
  const types = useQuery({ queryKey: ['admin', 'event', String(event.id), 'ticket-types'], queryFn: () => api<TicketType[]>(`/admin/events/${event.id}/ticket-types`) })
  const remove = useMutation({
    mutationFn: (p: Promotion) => api(`/admin/promotions/${p.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'event', String(event.id), 'promotions'] })
      toast.success('Promo code removed.')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const typeName = (id: number) => types.data?.find((t) => t.id === id)?.name ?? `#${id}`

  const columns: Column<Promotion>[] = [
    { header: 'Code', cell: (p) => <span className="font-mono font-semibold text-zinc-900">{p.code}</span> },
    { header: 'Discount', cell: (p) => describe(p, event.currency) },
    { header: 'Applies to', cell: (p) => (p.ticket_type_ids.length ? p.ticket_type_ids.map(typeName).join(', ') : 'All tickets') },
    { header: 'Used', cell: (p) => `${p.times_used}${p.max_uses != null ? ` / ${p.max_uses}` : ''}`, align: 'right' },
    { header: 'Status', cell: (p) => (p.is_active ? <Badge tone="green">Active</Badge> : <Badge>Inactive</Badge>) },
    {
      header: '',
      align: 'right',
      cell: (p) => (
        <div className="flex justify-end gap-1">
          <Button size="sm" variant="ghost" onClick={() => setEditing(p)} aria-label={`Edit ${p.code}`}><Pencil className="size-4" /></Button>
          <Button size="sm" variant="ghost" onClick={() => window.confirm(`Delete ${p.code}?`) && remove.mutate(p)} aria-label={`Delete ${p.code}`}><Trash2 className="size-4" /></Button>
        </div>
      ),
    },
  ]
  return (
    <Card>
      <CardHeader title="Promo codes" actions={<Button size="sm" icon={<Plus className="size-4" />} onClick={() => setEditing(null)}>New promo code</Button>} />
      <DataTable columns={columns} rows={promos.data} loading={promos.isLoading} rowKey={(p) => p.id} empty={{ title: 'No promo codes', description: 'Create codes for early birds, partners or groups.' }} />
      {editing !== undefined && <PromotionModal event={event} promotion={editing} ticketTypes={types.data ?? []} onClose={() => setEditing(undefined)} />}
    </Card>
  )
}
