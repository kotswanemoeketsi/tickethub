import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../../lib/api'
import { formatDateTime, formatMoney, fromCents, toCents } from '../../../lib/format'
import type { AdminEvent, TicketType } from '../../../lib/types'
import { Badge, Button, Card, CardHeader, Checkbox, Input, Modal, ProgressBar, Select, StatusBadge, Textarea, Alert } from '../../../components/ui'
import { DataTable, type Column } from '../../../components/DataTable'
import { useToast } from '../../../components/toast'

export const ticketCategories = ['general', 'vip', 'early_bird', 'student', 'group', 'complimentary', 'other'].map((c) => ({
  value: c,
  label: c === 'vip' ? 'VIP' : c.replace('_', ' ').replace(/^./, (x) => x.toUpperCase()),
}))

interface Form {
  name: string
  description: string
  category: string
  price: string
  quantity_total: string
  min_per_order: string
  max_per_order: string
  sales_start_at: string
  sales_end_at: string
  is_hidden: boolean
  sort_order: string
}

function toForm(t?: TicketType): Form {
  return {
    name: t?.name ?? '',
    description: t?.description ?? '',
    category: t?.category ?? 'general',
    price: t ? fromCents(t.price_cents) : '',
    quantity_total: t ? String(t.quantity_total) : '',
    min_per_order: String(t?.min_per_order ?? 1),
    max_per_order: String(t?.max_per_order ?? 10),
    sales_start_at: t?.sales_start_at_local ?? '',
    sales_end_at: t?.sales_end_at_local ?? '',
    is_hidden: t?.is_hidden ?? false,
    sort_order: String(t?.sort_order ?? 0),
  }
}

function TicketTypeModal({ event, ticketType, onClose }: { event: AdminEvent; ticketType: TicketType | null; onClose: () => void }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [form, setForm] = useState<Form>(() => toForm(ticketType ?? undefined))
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }))
  const save = useMutation({
    mutationFn: () => {
      const { price, ...rest } = form
      const body = {
        ...rest,
        price_cents: toCents(price),
        quantity_total: Number(form.quantity_total) || 0,
        min_per_order: Number(form.min_per_order) || 0,
        max_per_order: Number(form.max_per_order) || 0,
        sort_order: Number(form.sort_order) || 0,
      }
      return ticketType
        ? api(`/admin/ticket-types/${ticketType.id}`, { method: 'PUT', body })
        : api(`/admin/events/${event.id}/ticket-types`, { method: 'POST', body })
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'event', String(event.id)] })
      toast.success(ticketType ? 'Ticket type saved.' : 'Ticket type added.')
      onClose()
    },
  })
  const errors = fieldErrors(save.error)
  return (
    <Modal
      open
      onClose={onClose}
      size="lg"
      title={ticketType ? `Edit ${ticketType.name}` : 'New ticket type'}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button loading={save.isPending} onClick={() => save.mutate()}>Save</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        {save.error && !Object.keys(errors).length && <div className="sm:col-span-2"><Alert tone="error">{errorMessage(save.error)}</Alert></div>}
        <Input label="Name" value={form.name} onChange={(e) => set('name', e.target.value)} error={errors.name} placeholder="e.g. Early Bird" />
        <Select label="Category" options={ticketCategories} value={form.category} onChange={(e) => set('category', e.target.value)} error={errors.category} />
        <Textarea label="Description" className="sm:col-span-2" rows={2} value={form.description} onChange={(e) => set('description', e.target.value)} error={errors.description} />
        <Input label={`Price (${event.currency})`} inputMode="decimal" value={form.price} onChange={(e) => set('price', e.target.value)} error={errors.price_cents} hint="Use 0 for free tickets." />
        <Input
          label="Quantity available"
          type="number"
          min={0}
          value={form.quantity_total}
          onChange={(e) => set('quantity_total', e.target.value)}
          error={errors.quantity_total}
          hint={ticketType ? `${ticketType.quantity_sold} sold and ${ticketType.quantity_reserved} in checkout.` : undefined}
        />
        <Input label="Minimum per order" type="number" min={1} value={form.min_per_order} onChange={(e) => set('min_per_order', e.target.value)} error={errors.min_per_order} />
        <Input label="Maximum per order" type="number" min={1} value={form.max_per_order} onChange={(e) => set('max_per_order', e.target.value)} error={errors.max_per_order} />
        <Input label="Sales start (optional)" type="datetime-local" value={form.sales_start_at} onChange={(e) => set('sales_start_at', e.target.value)} error={errors.sales_start_at} hint={`In ${event.timezone}`} />
        <Input label="Sales end (optional)" type="datetime-local" value={form.sales_end_at} onChange={(e) => set('sales_end_at', e.target.value)} error={errors.sales_end_at} hint="Defaults to the event's end." />
        <Input label="Display order" type="number" value={form.sort_order} onChange={(e) => set('sort_order', e.target.value)} error={errors.sort_order} />
        <div className="flex items-end pb-2">
          <Checkbox label="Hidden" description="Not listed publicly; use for complimentary or staff tickets." checked={form.is_hidden} onChange={(v) => set('is_hidden', v)} />
        </div>
      </div>
    </Modal>
  )
}

export function TicketTypesTab({ event, canManage }: { event: AdminEvent; canManage: boolean }) {
  const queryClient = useQueryClient()
  const toast = useToast()
  const [editing, setEditing] = useState<TicketType | null | undefined>(undefined)
  const types = useQuery({ queryKey: ['admin', 'event', String(event.id), 'ticket-types'], queryFn: () => api<TicketType[]>(`/admin/events/${event.id}/ticket-types`) })
  const remove = useMutation({
    mutationFn: (t: TicketType) => api(`/admin/ticket-types/${t.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin', 'event', String(event.id)] })
      toast.success('Ticket type removed.')
    },
    onError: (e) => toast.error(errorMessage(e)),
  })
  const allocated = (types.data ?? []).reduce((sum, t) => sum + t.quantity_total, 0)

  const columns: Column<TicketType>[] = [
    {
      header: 'Ticket type',
      cell: (t) => (
        <div>
          <p className="font-medium text-zinc-900">{t.name} {t.is_hidden && <Badge>Hidden</Badge>}</p>
          <p className="text-xs text-zinc-500">{ticketCategories.find((c) => c.value === t.category)?.label}, {t.min_per_order}–{t.max_per_order} per order</p>
        </div>
      ),
    },
    { header: 'Price', cell: (t) => (t.price_cents === 0 ? 'Free' : formatMoney(t.price_cents, event.currency)), align: 'right' },
    {
      header: 'Sold',
      cell: (t) => (
        <div className="w-36">
          <p className="text-xs text-zinc-500">{t.quantity_sold} sold, {t.quantity_reserved} held, {t.quantity_available} left</p>
          <ProgressBar value={t.quantity_sold} max={t.quantity_total} className="mt-1 h-1.5" />
        </div>
      ),
    },
    {
      header: 'Sales',
      cell: (t) => (
        <div>
          <StatusBadge status={t.sale_status} />
          {t.sales_start_at && <p className="mt-0.5 text-xs text-zinc-500">From {formatDateTime(t.sales_start_at, event.timezone)}</p>}
        </div>
      ),
    },
    {
      header: '',
      align: 'right',
      cell: (t) =>
        canManage && (
          <div className="flex justify-end gap-1">
            <Button size="sm" variant="ghost" onClick={() => setEditing(t)} aria-label={`Edit ${t.name}`}><Pencil className="size-4" /></Button>
            {t.quantity_sold === 0 && t.quantity_reserved === 0 && (
              <Button size="sm" variant="ghost" onClick={() => window.confirm(`Delete ${t.name}?`) && remove.mutate(t)} aria-label={`Delete ${t.name}`}>
                <Trash2 className="size-4" />
              </Button>
            )}
          </div>
        ),
    },
  ]

  return (
    <Card>
      <CardHeader
        title="Ticket types"
        description={`${allocated} of ${event.capacity} capacity allocated`}
        actions={canManage && event.status !== 'cancelled' && <Button size="sm" icon={<Plus className="size-4" />} onClick={() => setEditing(null)}>Add ticket type</Button>}
      />
      <DataTable columns={columns} rows={types.data} loading={types.isLoading} rowKey={(t) => t.id} empty={{ title: 'No ticket types yet', description: 'Add at least one ticket type before submitting the event.' }} />
      {editing !== undefined && <TicketTypeModal event={event} ticketType={editing} onClose={() => setEditing(undefined)} />}
    </Card>
  )
}
