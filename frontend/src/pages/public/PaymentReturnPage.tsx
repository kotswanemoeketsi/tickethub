import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { CircleCheck, CircleX, LoaderCircle } from 'lucide-react'
import { api } from '../../lib/api'
import type { Order } from '../../lib/types'
import { ButtonLink, Card } from '../../components/ui'

/**
 * The payment gateway sends customers here after paying. The page asks the API to verify
 * the payment with the gateway (server to server) and then waits for the order to be paid;
 * nothing the browser reports is trusted on its own.
 */
export function PaymentReturnPage() {
  const { orderId = '' } = useParams()
  const [timedOut, setTimedOut] = useState(false)
  const started = useRef(Date.now())

  const order = useQuery({
    queryKey: ['order', orderId, 'verify'],
    queryFn: async () => {
      const o = await api<Order>(`/orders/${orderId}/verify-payment`, { method: 'POST' })
      if (Date.now() - started.current > 60_000) setTimedOut(true)
      return o
    },
    refetchInterval: (q) => (q.state.data?.status === 'pending' && !timedOut ? 2500 : false),
  })

  useEffect(() => {
    // Braces matter: newer browsers return a promise from scrollTo, which React would
    // otherwise treat as the effect's clean-up function.
    window.scrollTo(0, 0)
  }, [])

  const o = order.data
  const latest = o?.payments[0]
  const failed = o?.status === 'pending' && latest?.status === 'failed'
  const paid = o?.status === 'paid'

  return (
    <div className="mx-auto max-w-lg px-4 py-16">
      <Card className="p-8 text-center">
        {paid ? (
          <>
            <CircleCheck className="mx-auto size-14 text-emerald-500" />
            <h1 className="mt-4 text-2xl font-semibold">You're going to {o.event.title}!</h1>
            <p className="mt-2 text-zinc-600">Payment received. {o.tickets.length === 1 ? 'Your ticket is ready and on its way' : `Your ${o.tickets.length} tickets are ready and on their way`} to {o.customer_email}.</p>
            <div className="mt-6 flex flex-col gap-2">
              <ButtonLink to={`/account/orders/${o.id}`} size="lg">View my tickets</ButtonLink>
              <ButtonLink to="/" variant="ghost">Browse more events</ButtonLink>
            </div>
          </>
        ) : failed || (o && o.status !== 'pending') ? (
          <>
            <CircleX className="mx-auto size-14 text-red-500" />
            <h1 className="mt-4 text-2xl font-semibold">Payment not completed</h1>
            <p className="mt-2 text-zinc-600">{latest?.failure_reason || 'The payment was not successful, so you have not been charged.'}</p>
            <div className="mt-6 flex flex-col gap-2">
              {o?.status === 'pending' && <ButtonLink to={`/checkout/${o.id}`} size="lg">Try again</ButtonLink>}
              <ButtonLink to={o ? `/events/${o.event.slug}` : '/'} variant="ghost">Back to the event</ButtonLink>
            </div>
          </>
        ) : timedOut ? (
          <>
            <LoaderCircle className="mx-auto size-14 text-amber-500" />
            <h1 className="mt-4 text-2xl font-semibold">Still confirming your payment</h1>
            <p className="mt-2 text-zinc-600">This is taking longer than usual. We will email your tickets as soon as the payment is confirmed — you do not need to pay again.</p>
            <ButtonLink to={`/account/orders/${orderId}`} className="mt-6">View order</ButtonLink>
          </>
        ) : (
          <>
            <LoaderCircle className="mx-auto size-14 animate-spin text-brand-600" />
            <h1 className="mt-4 text-2xl font-semibold">Confirming your payment…</h1>
            <p className="mt-2 text-zinc-600">Please keep this page open. It usually takes a few seconds.</p>
          </>
        )}
      </Card>
    </div>
  )
}
