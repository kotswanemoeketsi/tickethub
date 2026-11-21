import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { BadgeDollarSign, ChartLine, QrCode } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../../lib/api'
import { useAuth } from '../../lib/auth'
import type { Organiser } from '../../lib/types'
import { Alert, Button, ButtonLink, Card, Input, StatusBadge, Textarea } from '../../components/ui'

export function BecomeOrganiserPage() {
  const { me, refreshMe } = useAuth()
  const [form, setForm] = useState({ name: '', email: me?.user.email ?? '', phone: '', description: '' })
  const mutation = useMutation({
    mutationFn: () => api<Organiser>('/organisers/apply', { method: 'POST', body: form }),
    onSuccess: () => refreshMe(),
  })
  const errors = fieldErrors(mutation.error)
  const existing = me?.organisers ?? []

  return (
    <div className="mx-auto grid max-w-5xl gap-10 px-4 py-12 lg:grid-cols-2">
      <div>
        <h1 className="text-3xl font-bold tracking-tight">Sell tickets to your events</h1>
        <p className="mt-3 text-lg text-zinc-600">Create events, set up ticket types and promo codes, and get paid — while we handle payments, ticket delivery and the door.</p>
        <ul className="mt-8 space-y-5">
          {[
            { icon: <BadgeDollarSign className="size-5" />, title: 'Simple pricing', text: 'A small commission per ticket sold. No monthly fees.' },
            { icon: <QrCode className="size-5" />, title: 'Fast, fraud-proof entry', text: 'Signed QR tickets and a phone scanner app for your door staff.' },
            { icon: <ChartLine className="size-5" />, title: 'Live reporting', text: 'Sales, attendance and payouts at a glance, exportable to Excel.' },
          ].map((f) => (
            <li key={f.title} className="flex gap-4">
              <div className="rounded-lg bg-brand-50 p-2 text-brand-700">{f.icon}</div>
              <div>
                <p className="font-medium">{f.title}</p>
                <p className="text-sm text-zinc-500">{f.text}</p>
              </div>
            </li>
          ))}
        </ul>
      </div>

      <Card className="p-6">
        {existing.length > 0 && (
          <div className="mb-6 space-y-2">
            <p className="text-sm font-medium text-zinc-700">Your organisations</p>
            {existing.map((o) => (
              <div key={o.id} className="flex items-center justify-between rounded-lg border border-zinc-200 px-3 py-2 text-sm">
                <span>{o.name}</span>
                <StatusBadge status={o.status} />
              </div>
            ))}
            {existing.some((o) => o.status === 'active') && <ButtonLink to="/admin" variant="secondary" className="mt-2 w-full">Open the organiser portal</ButtonLink>}
          </div>
        )}
        {mutation.isSuccess ? (
          <Alert tone="success" title="Application received">
            We'll review your application and email you once your organiser account is approved. You can then create events straight away.
          </Alert>
        ) : (
          <form onSubmit={(e) => { e.preventDefault(); mutation.mutate() }} className="space-y-4">
            <h2 className="text-lg font-semibold">Apply as an organiser</h2>
            {mutation.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(mutation.error)}</Alert>}
            <Input label="Business or brand name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} error={errors.name} required />
            <Input label="Contact email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} error={errors.email} required />
            <Input label="Phone" type="tel" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} error={errors.phone} />
            <Textarea label="Tell us about your events" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} error={errors.description} />
            <Button type="submit" className="w-full" size="lg" loading={mutation.isPending}>Submit application</Button>
          </form>
        )}
      </Card>
    </div>
  )
}
