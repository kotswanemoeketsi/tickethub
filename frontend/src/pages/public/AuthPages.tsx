import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { api, apiPage, errorMessage, fieldErrors } from '../../lib/api'
import type { PublicEvent } from '../../lib/types'
import { EventCard } from '../../components/events'
import { useAuth } from '../../lib/auth'
import { Alert, Button, Input, Loading } from '../../components/ui'
import { Logo } from '../../layouts/PublicLayout'

/** Only allow redirects to paths on this site. */
export function safeNext(next: string | null, fallback: string) {
  return next && next.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\') ? next : fallback
}

const ssoErrors: Record<string, string> = {
  cancelled: 'Google sign-in was cancelled.',
  account_inactive: 'This account has been deactivated. Please contact support.',
  email_unverified: 'Your Google account’s email address is not verified, so it cannot be used to sign in.',
  expired: 'The sign-in took too long. Please try again.',
  invalid_state: 'The sign-in could not be completed in this browser. Please try again.',
  verification_failed: 'Google sign-in could not be verified. Please try again.',
  unavailable: 'Google sign-in is not available right now.',
}

function useProviders() {
  return useQuery({ queryKey: ['auth', 'providers'], queryFn: () => api<{ google: boolean }>('/auth/providers'), staleTime: 300_000 })
}

function GoogleMark() {
  return (
    <svg viewBox="0 0 48 48" className="size-5" aria-hidden>
      <path fill="#FFC107" d="M43.6 20.5H42V20H24v8h11.3C33.7 32.7 29.2 36 24 36c-6.6 0-12-5.4-12-12s5.4-12 12-12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 12.9 4 4 12.9 4 24s8.9 20 20 20 20-8.9 20-20c0-1.3-.1-2.4-.4-3.5z" />
      <path fill="#FF3D00" d="m6.3 14.7 6.6 4.8C14.7 15.1 19 12 24 12c3.1 0 5.8 1.2 7.9 3.1l5.7-5.7C34 6.1 29.3 4 24 4 16.3 4 9.7 8.3 6.3 14.7z" />
      <path fill="#4CAF50" d="M24 44c5.2 0 9.9-2 13.4-5.2l-6.2-5.2C29.2 35.1 26.7 36 24 36c-5.2 0-9.6-3.3-11.3-8l-6.5 5C9.5 39.6 16.2 44 24 44z" />
      <path fill="#1976D2" d="M43.6 20.5H42V20H24v8h11.3c-.8 2.2-2.2 4.2-4.1 5.6l6.2 5.2C37 39.2 44 34 44 24c0-1.3-.1-2.4-.4-3.5z" />
    </svg>
  )
}

/** "Continue with Google", shown only when the server has Google sign-in configured. */
function GoogleButton({ next }: { next: string }) {
  const { data } = useProviders()
  if (!data?.google) return null
  return (
    <>
      <a
        href={`/api/v1/auth/google/start?next=${encodeURIComponent(next)}`}
        className="flex h-12 w-full items-center justify-center gap-3 rounded-full bg-white text-[15px] font-semibold text-ink-900 ring-1 ring-zinc-300 hover:bg-zinc-50"
      >
        <GoogleMark /> Continue with Google
      </a>
      <div className="my-6 flex items-center gap-3 text-sm text-zinc-400">
        <span className="h-px flex-1 bg-zinc-200" /> or <span className="h-px flex-1 bg-zinc-200" />
      </div>
    </>
  )
}

/** A real upcoming event, shown as a ticket beside the sign-in form. */
function FeaturedTicket() {
  const { data } = useQuery({
    queryKey: ['public', 'events', 'featured'],
    queryFn: () => apiPage<PublicEvent>('/public/events', { sort: 'popular', per_page: 5 }),
    staleTime: 60_000,
  })
  const event = data?.data.find((e) => !e.sold_out)
  if (!event) return null
  return (
    <div className="w-[22rem] -rotate-3 [--color-canvas:var(--color-ink-900)]">
      <EventCard event={event} />
    </div>
  )
}

function AuthShell({ title, subtitle, children, footer }: { title: string; subtitle?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  return (
    <div className="grid min-h-[calc(100vh-8rem)] lg:grid-cols-2">
      <div className="relative hidden overflow-hidden bg-ink-900 lg:block">
        <div className="absolute -left-20 top-1/4 size-96 rounded-full bg-brand-600/50 blur-3xl" aria-hidden />
        <div className="absolute bottom-0 right-0 size-80 rounded-full bg-marigold-500/20 blur-3xl" aria-hidden />
        <div className="relative flex h-full flex-col justify-between gap-12 p-14 text-white">
          <div className="flex flex-1 items-center justify-center"><FeaturedTicket /></div>
          <div>
          <p className="max-w-md font-display text-4xl font-bold leading-tight">Your tickets, on your phone, ready at the door.</p>
          <p className="mt-4 max-w-md text-ink-200">Buy in a few taps, transfer a ticket to a friend, and find every booking in one place.</p>
          </div>
        </div>
      </div>
      <div className="flex items-center justify-center px-4 py-12 sm:px-6">
        <div className="w-full max-w-md">
          <h1 className="text-3xl font-bold text-ink-900">{title}</h1>
          {subtitle && <p className="mt-2 text-zinc-500">{subtitle}</p>}
          <div className="mt-8">{children}</div>
          {footer && <p className="mt-8 text-center text-sm text-zinc-500">{footer}</p>}
        </div>
      </div>
    </div>
  )
}

export function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const next = safeNext(params.get('next'), '')
  const ssoError = params.get('sso_error')
  const mutation = useMutation({
    mutationFn: () => login(email, password),
    onSuccess: (me) => navigate(next || (me.staff_access ? '/admin' : '/'), { replace: true }),
  })
  const submit = (e: FormEvent) => {
    e.preventDefault()
    mutation.mutate()
  }
  return (
    <AuthShell
      title="Sign in"
      subtitle="Welcome back. Sign in to buy tickets and see your bookings."
      footer={<>New here? <Link to={`/register${next ? `?next=${encodeURIComponent(next)}` : ''}`} className="font-semibold text-brand-700">Create an account</Link></>}
    >
      {ssoError && <div className="mb-6"><Alert tone="error">{ssoErrors[ssoError] ?? ssoErrors.verification_failed}</Alert></div>}
      <GoogleButton next={next || '/'} />
      <form onSubmit={submit} className="space-y-4">
        {mutation.error && <Alert tone="error">{errorMessage(mutation.error)}</Alert>}
        <Input label="Email" type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <Input label="Password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        <div className="flex justify-end">
          <Link to="/forgot-password" className="text-sm font-medium text-brand-700">Forgot your password?</Link>
        </div>
        <Button type="submit" size="lg" className="w-full rounded-full" loading={mutation.isPending}>Sign in</Button>
      </form>
    </AuthShell>
  )
}

export function RegisterPage() {
  const { register } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'), '/')
  const [form, setForm] = useState({ first_name: '', last_name: '', email: '', phone: '', password: '' })
  const mutation = useMutation({ mutationFn: () => register(form), onSuccess: () => navigate(next, { replace: true }) })
  const errors = fieldErrors(mutation.error)
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }))
  return (
    <AuthShell
      title="Create your account"
      subtitle="It takes less than a minute."
      footer={<>Already have an account? <Link to={`/login${next !== '/' ? `?next=${encodeURIComponent(next)}` : ''}`} className="font-semibold text-brand-700">Sign in</Link></>}
    >
      <GoogleButton next={next} />
      <form onSubmit={(e) => { e.preventDefault(); mutation.mutate() }} className="space-y-4">
        {mutation.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(mutation.error)}</Alert>}
        <div className="grid gap-4 sm:grid-cols-2">
          <Input label="First name" autoComplete="given-name" value={form.first_name} onChange={set('first_name')} error={errors.first_name} required />
          <Input label="Last name" autoComplete="family-name" value={form.last_name} onChange={set('last_name')} error={errors.last_name} required />
        </div>
        <Input label="Email" type="email" autoComplete="email" value={form.email} onChange={set('email')} error={errors.email} required />
        <Input label="Mobile number (optional)" type="tel" autoComplete="tel" value={form.phone} onChange={set('phone')} error={errors.phone} />
        <Input label="Password" type="password" autoComplete="new-password" value={form.password} onChange={set('password')} error={errors.password} hint="At least 8 characters with a letter and a number." required />
        <Button type="submit" size="lg" className="w-full rounded-full" loading={mutation.isPending}>Create account</Button>
        <p className="text-center text-xs text-zinc-500">By creating an account you agree to the <Link to="/terms" className="underline">terms of sale</Link>.</p>
      </form>
    </AuthShell>
  )
}

export function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const mutation = useMutation({ mutationFn: () => api<{ message: string }>('/auth/forgot-password', { method: 'POST', body: { email } }) })
  return (
    <AuthShell
      title="Reset your password"
      subtitle="We'll email you a link to choose a new password. If you signed up with Google, this also lets you add a password."
      footer={<Link to="/login" className="font-semibold text-brand-700">Back to sign in</Link>}
    >
      {mutation.isSuccess ? (
        <Alert tone="success">{mutation.data.message}</Alert>
      ) : (
        <form onSubmit={(e) => { e.preventDefault(); mutation.mutate() }} className="space-y-4">
          {mutation.error && <Alert tone="error">{errorMessage(mutation.error)}</Alert>}
          <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
          <Button type="submit" size="lg" className="w-full rounded-full" loading={mutation.isPending}>Send reset link</Button>
        </form>
      )}
    </AuthShell>
  )
}

export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const [password, setPassword] = useState('')
  const mutation = useMutation({
    mutationFn: () => api<{ message: string }>('/auth/reset-password', { method: 'POST', body: { token: params.get('token') ?? '', password } }),
  })
  const errors = fieldErrors(mutation.error)
  return (
    <AuthShell title="Choose a new password">
      {mutation.isSuccess ? (
        <div className="space-y-4">
          <Alert tone="success">{mutation.data.message}</Alert>
          <Link to="/login" className="font-semibold text-brand-700">Sign in</Link>
        </div>
      ) : (
        <form onSubmit={(e) => { e.preventDefault(); mutation.mutate() }} className="space-y-4">
          {mutation.error && !errors.password && <Alert tone="error">{errorMessage(mutation.error)}</Alert>}
          <Input label="New password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} error={errors.password} hint="At least 8 characters with a letter and a number." required autoFocus />
          <Button type="submit" size="lg" className="w-full rounded-full" loading={mutation.isPending}>Save password</Button>
        </form>
      )}
    </AuthShell>
  )
}

/**
 * Google sends people back through the API, which sets the session cookie and redirects
 * here. Restoring the session from that cookie is all that is left to do.
 */
export function AuthCompletePage() {
  const { me, loading } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'), '/')
  // The session is restored from the cookie when the app loads; once that has finished,
  // either we are signed in or the cookie was not accepted.
  const failed = !loading && !me

  useEffect(() => {
    if (me) navigate(next === '/' && me.staff_access ? '/admin' : next, { replace: true })
  }, [me, next, navigate])

  if (failed) {
    return (
      <AuthShell title="Sign-in didn't finish">
        <Alert tone="error">We couldn't start your session. Please try again.</Alert>
        <Link to="/login" className="mt-6 inline-block font-semibold text-brand-700">Back to sign in</Link>
      </AuthShell>
    )
  }
  return (
    <div className="flex min-h-[50vh] flex-col items-center justify-center gap-4">
      <Logo />
      <Loading label="Signing you in…" />
    </div>
  )
}
