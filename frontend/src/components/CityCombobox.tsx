import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { ChevronDown, LoaderCircle } from 'lucide-react'
import { control, cx, Field } from './ui'

export interface CityOption {
  name: string
  /** Earlier names of a renamed town, which also match a search. */
  former_names?: string[]
  latitude?: number | null
  longitude?: number | null
}

// Rendering thousands of rows at once is slow, and nobody scrolls that far: past this many,
// the list asks the user to type instead.
const maxShown = 150

const fold = (s: string) => s.normalize('NFD').replace(/[̀-ͯ]/g, '').toLowerCase().trim()

/**
 * Names starting with the query first, then names containing it, each alphabetically.
 * A town's former names match too, so "Nelspruit" finds Mbombela.
 */
function filterCities(options: CityOption[], query: string): CityOption[] {
  const q = fold(query)
  if (!q) return options
  const starts: CityOption[] = []
  const contains: CityOption[] = []
  for (const o of options) {
    const names = [o.name, ...(o.former_names ?? [])].map(fold)
    if (names.some((n) => n.startsWith(q))) starts.push(o)
    else if (names.some((n) => n.includes(q))) contains.push(o)
  }
  return [...starts, ...contains]
}

/**
 * A searchable dropdown of cities. Opening it lists every city alphabetically; typing
 * narrows the list. With allowCustom, a name that isn't listed can be kept as typed.
 */
export function CityCombobox({ label, value, options, onChange, placeholder, disabled, loading, error, hint, allowCustom = false, className }: {
  label: string
  value: string
  options: CityOption[]
  onChange: (name: string, option?: CityOption) => void
  placeholder?: string
  disabled?: boolean
  loading?: boolean
  error?: string
  hint?: string
  allowCustom?: boolean
  className?: string
}) {
  const listId = useId()
  const listRef = useRef<HTMLUListElement>(null)
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState(value)
  const [active, setActive] = useState(0)

  // Follow the value when the parent changes it, such as a new province clearing the city.
  useEffect(() => setQuery(value), [value])

  const matches = useMemo(() => filterCities(options, query === value ? '' : query), [options, query, value])
  const shown = matches.slice(0, maxShown)
  const typed = query.trim()
  const exact = options.find((o) => fold(o.name) === fold(typed)) ?? options.find((o) => o.former_names?.some((n) => fold(n) === fold(typed)))
  const offerCustom = allowCustom && typed !== '' && !exact

  const choose = (option: CityOption) => {
    onChange(option.name, option)
    setQuery(option.name)
    setOpen(false)
  }
  // Leaving the field keeps a listed city (whatever its case), a custom name when allowed,
  // and otherwise goes back to the previous choice.
  const commit = () => {
    setOpen(false)
    if (exact) {
      if (exact.name !== value) onChange(exact.name, exact)
      setQuery(exact.name)
    } else if (allowCustom) {
      if (typed !== value) onChange(typed)
      setQuery(typed)
    } else {
      setQuery(value)
    }
  }

  // Opening the list starts at the current city, so it is in view and arrow keys move from it.
  useEffect(() => {
    if (open) setActive(Math.max(0, shown.findIndex((o) => o.name === value)))
    // Only when the list opens; typing resets the position itself.
  }, [open])

  useEffect(() => {
    if (!open) return
    listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active, open])

  const optionCount = shown.length + (offerCustom ? 1 : 0)
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        if (!open) setOpen(true)
        else setActive((i) => Math.min(i + 1, optionCount - 1))
        break
      case 'ArrowUp':
        e.preventDefault()
        setActive((i) => Math.max(i - 1, 0))
        break
      case 'Enter':
        if (!open) return
        e.preventDefault()
        if (active < shown.length) choose(shown[active])
        else if (offerCustom) commit()
        break
      case 'Escape':
        if (open) {
          e.preventDefault()
          setOpen(false)
          setQuery(value)
        }
        break
      case 'Tab':
        if (open) commit()
        break
    }
  }

  return (
    <Field label={label} error={error} hint={hint} className={className}>
      {(id) => (
        <div className="relative">
          <input
            id={id}
            role="combobox"
            aria-expanded={open}
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={open && optionCount ? `${listId}-${active}` : undefined}
            aria-invalid={!!error}
            autoComplete="off"
            spellCheck={false}
            disabled={disabled}
            placeholder={placeholder}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setActive(0)
              setOpen(true)
            }}
            onFocus={(e) => e.target.select()}
            onClick={() => setOpen(true)}
            onBlur={commit}
            onKeyDown={onKeyDown}
            className={cx(control, 'h-11 pr-10', error && 'ring-red-400')}
          />
          <button
            type="button"
            tabIndex={-1}
            aria-label={open ? `Close ${label} list` : `Open ${label} list`}
            disabled={disabled}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => setOpen(!open)}
            className="absolute inset-y-0 right-0 grid w-10 place-items-center text-zinc-400 disabled:opacity-50"
          >
            {loading ? <LoaderCircle className="size-4 animate-spin" /> : <ChevronDown className="size-4" />}
          </button>
          {open && !disabled && (
            <ul
              ref={listRef}
              id={listId}
              role="listbox"
              aria-label={label}
              className="absolute z-40 mt-1.5 max-h-72 w-full overflow-y-auto overscroll-contain rounded-xl bg-white py-1.5 shadow-xl ring-1 ring-zinc-200"
            >
              {shown.map((o, i) => (
                <li
                  key={o.name}
                  id={`${listId}-${i}`}
                  data-index={i}
                  role="option"
                  aria-selected={o.name === value}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => choose(o)}
                  onMouseMove={() => setActive(i)}
                  className={cx(
                    'cursor-pointer px-3.5 py-2 text-[15px]',
                    i === active ? 'bg-brand-50 text-brand-800' : 'text-zinc-800',
                    o.name === value && 'font-semibold',
                  )}
                >
                  {o.name}
                  {o.former_names?.length ? <span className="ml-2 text-sm font-normal text-zinc-500">formerly {o.former_names.join(', ')}</span> : null}
                </li>
              ))}
              {offerCustom && (
                <li
                  id={`${listId}-${shown.length}`}
                  data-index={shown.length}
                  role="option"
                  aria-selected={false}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={commit}
                  onMouseMove={() => setActive(shown.length)}
                  className={cx('cursor-pointer border-t border-zinc-100 px-3.5 py-2 text-[15px]', active === shown.length ? 'bg-brand-50 text-brand-800' : 'text-zinc-800')}
                >
                  Use “{typed}”
                </li>
              )}
              {loading && !options.length && <li className="px-3.5 py-2 text-sm text-zinc-500">Loading cities…</li>}
              {!loading && !optionCount && <li className="px-3.5 py-2 text-sm text-zinc-500">No city matches “{typed}”.</li>}
              {matches.length > shown.length && (
                <li className="border-t border-zinc-100 px-3.5 py-2 text-xs text-zinc-500">
                  Showing {shown.length.toLocaleString('en-ZA')} of {matches.length.toLocaleString('en-ZA')}. Type to find the rest.
                </li>
              )}
            </ul>
          )}
        </div>
      )}
    </Field>
  )
}
