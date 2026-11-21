import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'
import { ShieldAlert, WifiOff } from 'lucide-react'
import { useAuth } from '../lib/auth'
import { readWallet } from '../lib/wallet'
import { ButtonLink, EmptyState, Loading } from '../components/ui'

/**
 * Requires a signed-in user, and optionally any of the given permissions. Without a
 * connection the session can't be checked: the page shows offlineFallback if it has one,
 * and otherwise explains that it needs a connection, rather than asking the user to sign in.
 */
export function RequireAuth({ anyOf, staff, offlineFallback, children }: {
  anyOf?: string[]
  staff?: boolean
  offlineFallback?: ReactNode
  children: ReactNode
}) {
  const { me, loading, offline, can } = useAuth()
  const location = useLocation()
  if (loading) return <Loading />
  if (!me && offline) return <>{offlineFallback ?? <OfflineNotice />}</>
  if (!me) return <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />
  if ((staff && !me.staff_access) || (anyOf && !anyOf.some(can))) return <Forbidden />
  return <>{children}</>
}

export function Forbidden() {
  return (
    <EmptyState
      icon={<ShieldAlert className="size-6" />}
      title="You don't have access to this page"
      description="Ask an administrator to give your account the permission it needs."
      action={<ButtonLink to="/" variant="secondary">Go to the marketplace</ButtonLink>}
    />
  )
}

export function OfflineNotice() {
  const hasTickets = !!readWallet()?.tickets.length
  return (
    <EmptyState
      icon={<WifiOff className="size-6" />}
      title="You're offline"
      description={hasTickets ? 'This page needs a connection. Your saved tickets are still available.' : 'This page needs a connection. It will load once you are back online.'}
      action={hasTickets && <ButtonLink to="/account/tickets">Show my tickets</ButtonLink>}
    />
  )
}
