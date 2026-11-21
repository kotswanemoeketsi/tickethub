import { useState } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatMoney, fromCents, humanize, toCents } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { Payment } from '../../lib/types'
import { Alert, Button, Card, Input, Modal, PageHeader, Pagination, Select, StatusBadge, Textarea } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'

function ConfirmEFTModal({ payment, onClose }: { payment: Payment; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [amount, setAmount] = useState(fromCents(payment.amount_cents))
  const [bankRef, setBankRef] = useState('')
  const [note, setNote] = useState('')
  const confirm = useMutation({
    mutationFn: () => api(`/admin/payments/${payment.id}/confirm`, { method: 'POST', body: { amount_cents: toCents(amount), bank_reference: bankRef, note } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin'] })
      toast.success('Payment confirmed. Tickets are being issued.')
      onClose()
    },
  })
  const errors = fieldErrors(confirm.error)
  return (
    <Modal
      open
      onClose={onClose}
      title="Confirm EFT payment"
      description={`${payment.order_reference}, ${payment.customer_name}`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Cancel</Button>
          <Button variant="success" loading={confirm.isPending} onClick={() => confirm.mutate()}>Confirm and issue tickets</Button>
        </>
      }
    >
      <div className="space-y-4">
        <Alert tone="info">
          Check your bank statement for a deposit of <strong>{formatMoney(payment.amount_cents, payment.currency)}</strong> with reference <strong className="font-mono">{payment.order_reference}</strong>. Only confirm money that has cleared.
        </Alert>
        {confirm.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(confirm.error)}</Alert>}
        <Input label={`Amount received (${payment.currency})`} inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} error={errors.amount_cents} hint="Must match the order total." />
        <Input label="Bank statement reference" value={bankRef} onChange={(e) => setBankRef(e.target.value)} error={errors.bank_reference} required />
        <Textarea label="Note (optional)" rows={2} value={note} onChange={(e) => setNote(e.target.value)} error={errors.note} />
      </div>
    </Modal>
  )
}

export default function PaymentsPage() {
  const { can } = useAuth()
  // The API checks the permission against the payment's organiser; this only hides a button that can never work.
  const canConfirm = can(P.PaymentsConfirm)
  const [q, setQ] = useQueryParam('q')
  const [status, setStatus] = useQueryParam('status')
  const [method, setMethod] = useQueryParam('method')
  const [page, setPage] = useQueryParam('page', '1')
  const [confirming, setConfirming] = useState<Payment | null>(null)
  const query = useDebounced(q, 300)
  const payments = useQuery({
    queryKey: ['admin', 'payments', { query, status, method, page }],
    queryFn: () => apiPage<Payment>('/admin/payments', { q: query, status, method, page }),
  })

  const columns: Column<Payment>[] = [
    { header: 'Reference', cell: (p) => <div><Link to={`/admin/orders/${p.order_id}`} className="font-mono font-medium text-zinc-900 hover:text-brand-700">{p.order_reference}</Link><p className="font-mono text-xs text-zinc-500">{p.provider_reference}</p></div> },
    { header: 'Customer', cell: (p) => <div><p>{p.customer_name}</p><p className="text-xs text-zinc-500">{p.event_title}</p></div> },
    { header: 'Method', cell: (p) => (p.method === 'eft' ? 'EFT' : humanize(p.provider)) },
    { header: 'Amount', cell: (p) => formatMoney(p.amount_cents, p.currency), align: 'right' },
    { header: 'Status', cell: (p) => <div><StatusBadge status={p.status} />{p.refunded_cents > 0 && <p className="mt-0.5 text-xs text-purple-700">{formatMoney(p.refunded_cents, p.currency)} refunded</p>}</div> },
    { header: 'Date', cell: (p) => <div><p>{formatDateTime(p.paid_at ?? p.created_at)}</p>{p.status === 'pending' && p.order_expires_at && <p className="text-xs text-amber-700">Pay by {formatDateTime(p.order_expires_at)}</p>}</div> },
    {
      header: '',
      align: 'right',
      cell: (p) =>
        p.method === 'eft' && p.status === 'pending' && p.order_status === 'pending' && canConfirm ? (
          <Button size="sm" variant="success" onClick={() => setConfirming(p)}>Confirm</Button>
        ) : null,
    },
  ]

  return (
    <>
      <PageHeader title="Payments" description="Online and EFT payments. EFT payments are confirmed here once the money reflects in the bank." />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Reference, name or email" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search payments" />
          <Select aria-label="Status" className="w-44" placeholder="All statuses" value={status} onChange={(e) => setStatus(e.target.value)} options={['pending', 'succeeded', 'failed', 'cancelled'].map((s) => ({ value: s, label: humanize(s) }))} />
          <Select aria-label="Method" className="w-44" placeholder="All methods" value={method} onChange={(e) => setMethod(e.target.value)} options={[{ value: 'online', label: 'Online' }, { value: 'eft', label: 'EFT' }]} />
        </div>
        <DataTable columns={columns} rows={payments.data?.data} loading={payments.isLoading} rowKey={(p) => p.id} empty={{ title: 'No payments found' }} />
        <Pagination meta={payments.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
      {confirming && <ConfirmEFTModal payment={confirming} onClose={() => setConfirming(null)} />}
    </>
  )
}
