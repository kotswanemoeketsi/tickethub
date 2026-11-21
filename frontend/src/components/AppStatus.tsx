import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useRegisterSW } from 'virtual:pwa-register/react'
import { RefreshCw, WifiOff, X } from 'lucide-react'
import { api } from '../lib/api'
import { useAuth } from '../lib/auth'
import { useOnline } from '../lib/pwa'
import type { Ticket } from '../lib/types'
import { saveWallet } from '../lib/wallet'
import { Button } from './ui'

// Installed apps can stay open for days, so check for a new release every hour.
const updateCheckInterval = 60 * 60 * 1000

/** Offers to reload when a new version of the app has been downloaded. */
function UpdatePrompt() {
  const {
    needRefresh: [needRefresh, setNeedRefresh],
    updateServiceWorker,
  } = useRegisterSW({
    onRegisteredSW(_url, registration) {
      if (!registration) return
      setInterval(() => {
        if (navigator.onLine) registration.update().catch(() => {})
      }, updateCheckInterval)
    },
  })
  if (!needRefresh) return null
  return (
    <div role="status" className="pointer-events-auto flex items-center gap-3 rounded-2xl bg-ink-900 py-2.5 pl-4 pr-2.5 text-sm text-white shadow-xl">
      <span>A new version of the app is ready.</span>
      <Button size="sm" icon={<RefreshCw className="size-4" />} onClick={() => updateServiceWorker(true)}>Reload</Button>
      <button onClick={() => setNeedRefresh(false)} aria-label="Not now" className="rounded-full p-1.5 text-white/70 hover:bg-white/10 hover:text-white">
        <X className="size-4" />
      </button>
    </div>
  )
}

function OfflineBanner() {
  const online = useOnline()
  if (online) return null
  return (
    <div role="status" className="pointer-events-auto flex items-center gap-2 rounded-full bg-zinc-800 px-4 py-2 text-sm text-white shadow-lg">
      <WifiOff className="size-4" /> You're offline. Saved tickets still work.
    </div>
  )
}

/**
 * Keeps the offline copy of the signed-in user's tickets current, so they are on the
 * device even if the user never opened My tickets before losing signal.
 */
function WalletSync() {
  const { me } = useAuth()
  const online = useOnline()
  const queryClient = useQueryClient()
  const userId = me?.user.id
  useEffect(() => {
    if (!userId || !online) return
    queryClient
      .fetchQuery({ queryKey: ['my-tickets', 'upcoming'], queryFn: () => api<Ticket[]>('/tickets', { query: { when: '' } }), staleTime: 60_000 })
      .then((tickets) => saveWallet(userId, tickets))
      .catch(() => {})
  }, [userId, online, queryClient])
  return null
}

/** App-wide status: update and offline notices, and the offline ticket copy. */
export function AppStatus() {
  return (
    <>
      <WalletSync />
      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-[60] flex flex-col items-center gap-2 px-4 pb-[env(safe-area-inset-bottom)]">
        <OfflineBanner />
        <UpdatePrompt />
      </div>
    </>
  )
}
