import { useState } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import { formatDateTime, formatMoney, humanize } from '../../lib/format'
import { useDebounced, useQueryParam } from '../../lib/hooks'
import { P } from '../../lib/permissions'
import type { Refund } from '../../lib/types'
import { Alert, Button, Card, Input, Modal, PageHeader, Pagination, Select, StatusBadge, Textarea } from '../../components/ui'
import { DataTable, type Column } from '../../components/DataTable'
import { useToast } from '../../components/toast'

type Step = 'approve' | 'reject' | 'process'

function RefundActionModal({ refund, step, onClose }: { refund: Refund; step: Step; onClose: () => void }) {
  const toast = useToast()
  const queryClient = useQueryClient()
  const [text, setText] = useState('')
  const eft = refund.payment_method === 'eft'
  const run = useMutation({
    mutationFn: () =>
      api(`/admin/refunds/${refund.id}/${step}`, {
        method: 'POST',
        body: step === 'process' ? { bank_reference: text } : { note: text },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin'] })
      toast.success({ approve: 'Refund approved.', reject: 'Refund rejected. The tickets remain valid.', process: 'Refund processed.' }[step])
      onClose()
    },
  })
  const errors = fieldErrors(run.error)
  const titles = { approve: 'Approve refund', reject: 'Reject refund', process: eft ? 'Record EFT refund' : 'Send refund to the gateway' }
  return (
    <Modal
      open
      onClose={onClose}
      title={titles[step]}
      description={`${refund.reference}, ${formatMoney(refund.amount_cents, refund.currency)} to ${refund.customer_name}`}
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>Back</Button>
          <Button variant={step === 'reject' ? 'danger' : 'primary'} loading={run.isPending} disabled={step === 'process' && eft && !text.trim()} onClick={() => run.mutate()}>
            {titles[step]}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {run.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(run.error)}</Alert>}
        {refund.reason && <p className="text-sm text-zinc-600"><span className="font-medium">Customer's reason:</span> {refund.reason}</p>}
        {step === 'process' ? (
          eft ? (
            <>
              <Alert tone="info">Pay the customer back by bank transfer first, then record the transfer reference here.</Alert>
              <Input label="Bank transfer reference" value={text} onChange={(e) => setText(e.target.value)} error={errors.bank_reference} />
            </>
          ) : (
            <Alert tone="info">The refund is sent to the payment gateway, which returns the money to the customer's card or account.</Alert>
          )
        ) : (
          <Textarea label={step === 'reject' ? 'Reason (sent to the customer)' : 'Note (optional)'} rows={3} value={text} onChange={(e) => setText(e.target.value)} error={errors.note} />
        )}
      </div>
    </Modal>
  )
}

export default function RefundsPage() {
  const { can } = useAuth()
  const [q, setQ] = useQueryParam('q')
  const [status, setStatus] = useQueryParam('status')
  const [page, setPage] = useQueryParam('page', '1')
  const [action, setAction] = useState<{ refund: Refund; step: Step } | null>(null)
  const query = useDebounced(q, 300)
  const refunds = useQuery({
    queryKey: ['admin', 'refunds', { query, status, page }],
    queryFn: () => apiPage<Refund>('/admin/refunds', { q: query, status, page }),
  })
  // The API enforces per-organiser permissions; the buttons only hide what can never work.
  const canReview = can(P.RefundsManage)
  const canProcess = can(P.RefundsProcess)

  const columns: Column<Refund>[] = [
    { header: 'Refund', cell: (r) => <div><p className="font-mono font-medium text-zinc-900">{r.reference}</p><p className="text-xs text-zinc-500">{formatDateTime(r.created_at)}, {humanize(r.initiated_by)}</p></div> },
    { header: 'Order', cell: (r) => <div><Link to={`/admin/orders/${r.order_id}`} className="font-mono hover:text-brand-700">{r.order_reference}</Link><p className="text-xs text-zinc-500">{r.customer_name}, {r.event_title}</p></div> },
    { header: 'Amount', cell: (r) => <div className="text-right"><p>{formatMoney(r.amount_cents, r.currency)}</p><p className="text-xs text-zinc-500">{r.ticket_ids.length} ticket{r.ticket_ids.length === 1 ? '' : 's'}, {r.payment_method === 'eft' ? 'EFT' : 'Online'}</p></div>, align: 'right' },
    { header: 'Status', cell: (r) => <div><StatusBadge status={r.status} />{r.failure_reason && <p className="mt-0.5 max-w-48 text-xs text-red-600">{r.failure_reason}</p>}</div> },
    {
      header: '',
      align: 'right',
      cell: (r) => (
        <div className="flex justify-end gap-1">
          {r.status === 'requested' && canReview && (
            <>
              <Button size="sm" variant="ghost" onClick={() => setAction({ refund: r, step: 'reject' })}>Reject</Button>
              <Button size="sm" onClick={() => setAction({ refund: r, step: 'approve' })}>Approve</Button>
            </>
          )}
          {(r.status === 'approved' || r.status === 'failed') && canProcess && <Button size="sm" onClick={() => setAction({ refund: r, step: 'process' })}>{r.status === 'failed' ? 'Retry' : 'Process'}</Button>}
        </div>
      ),
    },
  ]

  return (
    <>
      <PageHeader title="Refunds" description="Requested refunds are reviewed, then processed through the gateway or by bank transfer." />
      <Card>
        <div className="flex flex-wrap gap-3 border-b border-zinc-100 p-4">
          <Input placeholder="Reference, name or email" value={q} onChange={(e) => setQ(e.target.value)} className="w-full sm:w-72" aria-label="Search refunds" />
          <Select aria-label="Status" className="w-44" placeholder="All statuses" value={status} onChange={(e) => setStatus(e.target.value)} options={['requested', 'approved', 'processing', 'completed', 'rejected', 'failed'].map((s) => ({ value: s, label: humanize(s) }))} />
        </div>
        <DataTable columns={columns} rows={refunds.data?.data} loading={refunds.isLoading} rowKey={(r) => r.id} empty={{ title: 'No refunds found' }} />
        <Pagination meta={refunds.data?.meta} onPage={(p) => setPage(String(p))} />
      </Card>
      {action && <RefundActionModal refund={action.refund} step={action.step} onClose={() => setAction(null)} />}
    </>
  )
}
