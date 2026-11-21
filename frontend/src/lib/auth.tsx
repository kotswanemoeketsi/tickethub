import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { api, lastRefreshUnreachable, onExpiredSession, refreshSession, setAccessToken } from './api'
import type { Me, Session } from './types'
import { clearWallet } from './wallet'

interface RegisterInput {
  first_name: string
  last_name: string
  email: string
  phone: string
  password: string
}

interface AuthContextValue {
  me: Me | null
  loading: boolean
  /** The session couldn't be checked because the server was unreachable. */
  offline: boolean
  login: (email: string, password: string) => Promise<Me>
  register: (input: RegisterInput) => Promise<Me>
  logout: () => Promise<void>
  refreshMe: () => Promise<void>
  /** The user holds the permission somewhere (platform or any organiser). */
  can: (permission: string) => boolean
  /** The user holds the permission for data owned by a specific organiser. */
  canFor: (organiserId: number, permission: string) => boolean
  /** The user holds the permission through a platform role. */
  canPlatform: (permission: string) => boolean
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null)
  const [loading, setLoading] = useState(true)
  const [offline, setOffline] = useState(false)
  const queryClient = useQueryClient()

  const loadMe = useCallback(async () => {
    const profile = await api<Me>('/auth/me')
    // Saved offline tickets belong to one person; drop anyone else's.
    clearWallet(profile.user.id)
    setMe(profile)
    setOffline(false)
    return profile
  }, [])

  // Restore the session from the refresh cookie on page load, and again when the device
  // comes back online after starting without a connection.
  useEffect(() => {
    let cancelled = false
    onExpiredSession(() => {
      setAccessToken(null)
      setMe(null)
    })
    const restore = () =>
      refreshSession()
        .then(async (token) => {
          if (cancelled) return
          if (token) await loadMe()
          else if (lastRefreshUnreachable()) setOffline(true)
          else {
            // Signed out, perhaps from another device: the offline copy goes too.
            clearWallet()
            setOffline(false)
          }
        })
        .catch(() => !cancelled && setMe(null))
        .finally(() => !cancelled && setLoading(false))
    restore()
    const onOnline = () => {
      if (lastRefreshUnreachable()) restore()
    }
    window.addEventListener('online', onOnline)
    return () => {
      cancelled = true
      window.removeEventListener('online', onOnline)
    }
  }, [loadMe])

  const startSession = useCallback(
    async (session: Session) => {
      setAccessToken(session.access_token)
      queryClient.clear()
      return loadMe()
    },
    [loadMe, queryClient],
  )

  const value = useMemo<AuthContextValue>(() => {
    const platform = new Set(me?.permissions ?? [])
    const byOrganiser = new Map((me?.organisers ?? []).map((o) => [o.id, new Set(o.permissions)]))
    return {
      me,
      loading,
      offline,
      login: async (email, password) => startSession(await api<Session>('/auth/login', { method: 'POST', body: { email, password } })),
      register: async (input) => startSession(await api<Session>('/auth/register', { method: 'POST', body: input })),
      logout: async () => {
        try {
          await api('/auth/logout', { method: 'POST' })
        } finally {
          setAccessToken(null)
          setMe(null)
          clearWallet()
          queryClient.clear()
        }
      },
      refreshMe: async () => {
        await loadMe()
      },
      can: (permission) => platform.has(permission) || [...byOrganiser.values()].some((set) => set.has(permission)),
      canFor: (organiserId, permission) =>
        (!!me?.all_organisers && platform.has(permission)) || !!byOrganiser.get(organiserId)?.has(permission),
      canPlatform: (permission) => platform.has(permission),
    }
  }, [me, loading, offline, startSession, loadMe, queryClient])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
