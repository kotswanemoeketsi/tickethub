// The shared UI kit. Components are intentionally small and unopinionated so pages read
// clearly; styling is Tailwind throughout.

import {
  useEffect,
  useId,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react'
import { Link } from 'react-router'
import { ChevronLeft, ChevronRight, Inbox, LoaderCircle, X } from 'lucide-react'
import { humanize } from '../lib/format'
import type { PageMeta } from '../lib/types'

export function cx(...classes: (string | false | null | undefined)[]) {
  return classes.filter(Boolean).join(' ')
}

// ---- Buttons --------------------------------------------------------------------------

type Variant = 'primary' | 'secondary' | 'danger' | 'ghost' | 'success' | 'dark'
type Size = 'sm' | 'md' | 'lg'

const variants: Record<Variant, string> = {
  primary: 'bg-brand-600 text-white hover:bg-brand-700 shadow-sm shadow-brand-900/10',
  dark: 'bg-ink-900 text-white hover:bg-ink-800',
  secondary: 'bg-white text-zinc-800 ring-1 ring-inset ring-zinc-300 hover:bg-zinc-50',
  danger: 'bg-red-600 text-white hover:bg-red-700 shadow-sm',
  success: 'bg-emerald-600 text-white hover:bg-emerald-700 shadow-sm',
  ghost: 'text-zinc-700 hover:bg-zinc-100',
}
const sizes: Record<Size, string> = {
  sm: 'h-8 px-3 text-sm gap-1.5 rounded-lg',
  md: 'h-10 px-4 text-sm gap-2 rounded-xl',
  lg: 'h-12 px-6 text-[15px] gap-2 rounded-xl',
}

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
  icon?: ReactNode
}

export function Button({ variant = 'primary', size = 'md', loading, icon, className, children, disabled, ...rest }: ButtonProps) {
  return (
    <button
      {...rest}
      disabled={disabled || loading}
      className={cx(
        'inline-flex items-center justify-center font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50',
        variants[variant],
        sizes[size],
        className,
      )}
    >
      {loading ? <LoaderCircle className="size-4 animate-spin" /> : icon}
      {children}
    </button>
  )
}

export function ButtonLink({ to, variant = 'primary', size = 'md', className, children }: { to: string; variant?: Variant; size?: Size; className?: string; children: ReactNode }) {
  return (
    <Link to={to} className={cx('inline-flex items-center justify-center font-semibold transition-colors', variants[variant], sizes[size], className)}>
      {children}
    </Link>
  )
}

// ---- Form controls --------------------------------------------------------------------

export const control =
  'block w-full rounded-xl border-0 bg-white px-3.5 py-2 text-[15px] text-zinc-900 ring-1 ring-inset ring-zinc-300 placeholder:text-zinc-400 focus:ring-2 focus:ring-inset focus:ring-brand-600 disabled:bg-zinc-100 disabled:text-zinc-500'

interface FieldProps {
  label?: string
  error?: string
  hint?: string
  className?: string
  children: (id: string) => ReactNode
}

export function Field({ label, error, hint, className, children }: FieldProps) {
  const id = useId()
  return (
    <div className={className}>
      {label && (
        <label htmlFor={id} className="mb-1.5 block text-sm font-medium text-zinc-700">
          {label}
        </label>
      )}
      {children(id)}
      {error ? <p className="mt-1 text-sm text-red-600">{error}</p> : hint ? <p className="mt-1 text-xs text-zinc-500">{hint}</p> : null}
    </div>
  )
}

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  label?: string
  error?: string
  hint?: string
}

export function Input({ label, error, hint, className, ...rest }: InputProps) {
  return (
    <Field label={label} error={error} hint={hint} className={className}>
      {(id) => <input id={id} {...rest} className={cx(control, 'h-11', error && 'ring-red-400')} aria-invalid={!!error} />}
    </Field>
  )
}

interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label?: string
  error?: string
  hint?: string
}

export function Textarea({ label, error, hint, className, ...rest }: TextareaProps) {
  return (
    <Field label={label} error={error} hint={hint} className={className}>
      {(id) => <textarea id={id} rows={4} {...rest} className={cx(control, error && 'ring-red-400')} aria-invalid={!!error} />}
    </Field>
  )
}

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label?: string
  error?: string
  hint?: string
  options: { value: string | number; label: string }[]
  placeholder?: string
}

export function Select({ label, error, hint, options, placeholder, className, ...rest }: SelectProps) {
  return (
    <Field label={label} error={error} hint={hint} className={className}>
      {(id) => (
        <select id={id} {...rest} className={cx(control, 'h-11 pr-8', error && 'ring-red-400')}>
          {placeholder !== undefined && <option value="">{placeholder}</option>}
          {options.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      )}
    </Field>
  )
}

export function Checkbox({ label, description, checked, onChange, disabled }: { label: string; description?: string; checked: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  const id = useId()
  return (
    <div className="flex items-start gap-3">
      <input
        id={id}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5 size-4 rounded border-zinc-300 text-brand-600 focus:ring-brand-600"
      />
      <label htmlFor={id} className="text-sm">
        <span className="font-medium text-zinc-800">{label}</span>
        {description && <span className="block text-zinc-500">{description}</span>}
      </label>
    </div>
  )
}

// ---- Layout ---------------------------------------------------------------------------

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx('rounded-2xl border border-zinc-200/80 bg-white shadow-[0_1px_2px_rgba(21,18,58,0.04)]', className)}>{children}</div>
}

export function CardHeader({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-100 px-5 py-4">
      <div>
        <h3 className="font-sans text-base font-semibold text-ink-900">{title}</h3>
        {description && <p className="mt-0.5 text-sm text-zinc-500">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  )
}

export function PageHeader({ title, description, actions, back }: { title: ReactNode; description?: ReactNode; actions?: ReactNode; back?: { to: string; label: string } }) {
  return (
    <div className="mb-6">
      {back && (
        <Link to={back.to} className="mb-2 inline-flex items-center gap-1 text-sm text-zinc-500 hover:text-zinc-800">
          <ChevronLeft className="size-4" /> {back.label}
        </Link>
      )}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="min-w-0">
          <h1 className="text-[1.75rem] font-bold leading-tight text-ink-900">{title}</h1>
          {description && <p className="mt-1 text-sm text-zinc-500">{description}</p>}
        </div>
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </div>
    </div>
  )
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle className={cx('size-5 animate-spin text-zinc-400', className)} />
}

export function Loading({ label = 'Loading…' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-3 py-16 text-sm text-zinc-500">
      <Spinner /> {label}
    </div>
  )
}

export function EmptyState({ title, description, action, icon }: { title: string; description?: string; action?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-14 text-center">
      <div className="mb-3 rounded-full bg-zinc-100 p-3 text-zinc-500">{icon ?? <Inbox className="size-6" />}</div>
      <p className="font-medium text-zinc-900">{title}</p>
      {description && <p className="mt-1 max-w-sm text-sm text-zinc-500">{description}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  )
}

type Tone = 'gray' | 'green' | 'yellow' | 'red' | 'blue' | 'indigo' | 'purple'
const tones: Record<Tone, string> = {
  gray: 'bg-zinc-100 text-zinc-700 ring-zinc-500/10',
  green: 'bg-emerald-50 text-emerald-700 ring-emerald-600/20',
  yellow: 'bg-amber-50 text-amber-800 ring-amber-600/20',
  red: 'bg-red-50 text-red-700 ring-red-600/10',
  blue: 'bg-sky-50 text-sky-700 ring-sky-700/10',
  indigo: 'bg-brand-50 text-brand-700 ring-brand-700/10',
  purple: 'bg-purple-50 text-purple-700 ring-purple-700/10',
}

export function Badge({ tone = 'gray', children }: { tone?: Tone; children: ReactNode }) {
  return <span className={cx('inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset whitespace-nowrap', tones[tone])}>{children}</span>
}

const statusTones: Record<string, Tone> = {
  // events
  draft: 'gray', pending_review: 'yellow', published: 'green', upcoming: 'indigo', live: 'green', completed: 'gray', rejected: 'red', cancelled: 'red',
  // orders and payments
  pending: 'yellow', paid: 'green', expired: 'gray', refunded: 'purple', partially_refunded: 'purple', succeeded: 'green', failed: 'red',
  // refunds
  requested: 'yellow', approved: 'blue', processing: 'blue',
  // tickets and check-in
  valid: 'green', void: 'red', checked_in: 'green', checked_out: 'blue', not_checked_in: 'gray', accepted: 'green',
  // accounts and organisers
  active: 'green', inactive: 'gray', suspended: 'red',
  // sales
  on_sale: 'green', scheduled: 'blue', ended: 'gray', sold_out: 'red', hidden: 'gray',
}

export function StatusBadge({ status }: { status: string }) {
  return <Badge tone={statusTones[status] ?? 'gray'}>{humanize(status)}</Badge>
}

export function Alert({ tone = 'info', title, children }: { tone?: 'info' | 'warning' | 'error' | 'success'; title?: string; children?: ReactNode }) {
  const styles = {
    info: 'border-sky-200 bg-sky-50 text-sky-900',
    warning: 'border-amber-200 bg-amber-50 text-amber-900',
    error: 'border-red-200 bg-red-50 text-red-900',
    success: 'border-emerald-200 bg-emerald-50 text-emerald-900',
  }[tone]
  return (
    <div className={cx('rounded-lg border px-4 py-3 text-sm', styles)} role={tone === 'error' ? 'alert' : undefined}>
      {title && <p className="font-medium">{title}</p>}
      {children && <div className={title ? 'mt-1' : ''}>{children}</div>}
    </div>
  )
}

export function Stat({ label, value, hint, icon }: { label: string; value: ReactNode; hint?: ReactNode; icon?: ReactNode }) {
  return (
    <Card className="p-5">
      <div className="flex items-start justify-between gap-2">
        <p className="text-sm text-zinc-500">{label}</p>
        {icon && <div className="flex size-9 items-center justify-center rounded-xl bg-brand-50 text-brand-700">{icon}</div>}
      </div>
      <p className="mt-1 font-display text-[1.75rem] font-bold tracking-tight text-ink-900">{value}</p>
      {hint && <p className="mt-1 text-xs text-zinc-500">{hint}</p>}
    </Card>
  )
}

export function ProgressBar({ value, max, className }: { value: number; max: number; className?: string }) {
  const pct = max > 0 ? Math.min(100, Math.round((value / max) * 100)) : 0
  return (
    <div className={cx('h-2 overflow-hidden rounded-full bg-zinc-100', className)} role="progressbar" aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
      <div className="h-full rounded-full bg-brand-600 transition-all" style={{ width: `${pct}%` }} />
    </div>
  )
}

// ---- Tabs, pagination and dialogs -----------------------------------------------------

export function Tabs<T extends string>({ tabs, value, onChange }: { tabs: { value: T; label: ReactNode }[]; value: T; onChange: (v: T) => void }) {
  return (
    <div className="mb-5 flex gap-1 overflow-x-auto border-b border-zinc-200">
      {tabs.map((t) => (
        <button
          key={t.value}
          onClick={() => onChange(t.value)}
          className={cx(
            '-mb-px whitespace-nowrap border-b-2 px-3 py-2.5 text-sm font-medium transition-colors',
            t.value === value ? 'border-brand-600 text-brand-700' : 'border-transparent text-zinc-500 hover:text-zinc-800',
          )}
        >
          {t.label}
        </button>
      ))}
    </div>
  )
}

export function Pagination({ meta, onPage }: { meta?: PageMeta; onPage: (page: number) => void }) {
  if (!meta || meta.total <= meta.per_page) return null
  const pages = Math.ceil(meta.total / meta.per_page)
  const from = (meta.page - 1) * meta.per_page + 1
  const to = Math.min(meta.total, meta.page * meta.per_page)
  return (
    <div className="flex items-center justify-between border-t border-zinc-100 px-4 py-3 text-sm text-zinc-500">
      <span>
        {from}–{to} of {meta.total}
      </span>
      <div className="flex gap-1">
        <Button size="sm" variant="secondary" disabled={meta.page <= 1} onClick={() => onPage(meta.page - 1)} aria-label="Previous page">
          <ChevronLeft className="size-4" />
        </Button>
        <Button size="sm" variant="secondary" disabled={meta.page >= pages} onClick={() => onPage(meta.page + 1)} aria-label="Next page">
          <ChevronRight className="size-4" />
        </Button>
      </div>
    </div>
  )
}

export function Modal({ open, onClose, title, description, children, footer, size = 'md' }: {
  open: boolean
  onClose: () => void
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  footer?: ReactNode
  size?: 'sm' | 'md' | 'lg' | 'xl'
}) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    document.addEventListener('keydown', onKey)
    document.body.style.overflow = 'hidden'
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = ''
    }
  }, [open, onClose])
  if (!open) return null
  const width = { sm: 'max-w-md', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-4xl' }[size]
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-zinc-900/40 p-0 sm:items-center sm:p-4" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        className={cx('flex max-h-[92vh] w-full flex-col rounded-t-2xl bg-white shadow-xl sm:rounded-2xl', width)}
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-4 border-b border-zinc-100 px-6 py-4">
          <div>
            <h2 className="text-lg font-semibold text-zinc-900">{title}</h2>
            {description && <p className="mt-0.5 text-sm text-zinc-500">{description}</p>}
          </div>
          <button onClick={onClose} className="rounded-md p-1 text-zinc-400 hover:bg-zinc-100 hover:text-zinc-600" aria-label="Close">
            <X className="size-5" />
          </button>
        </div>
        <div className="overflow-y-auto px-6 py-5">{children}</div>
        {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-zinc-100 px-6 py-4">{footer}</div>}
      </div>
    </div>
  )
}

export function DescriptionList({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="divide-y divide-zinc-100">
      {items.map((item) => (
        <div key={item.label} className="grid grid-cols-3 gap-4 py-2.5 text-sm">
          <dt className="text-zinc-500">{item.label}</dt>
          <dd className="col-span-2 text-zinc-900">{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}
