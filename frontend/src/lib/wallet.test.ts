import { describe, expect, it } from 'vitest'
import { clearWallet, readWallet, saveWallet } from './wallet'
import type { Ticket } from './types'

class MemoryStorage implements Storage {
  private items = new Map<string, string>()
  get length() {
    return this.items.size
  }
  clear() {
    this.items.clear()
  }
  getItem(key: string) {
    return this.items.get(key) ?? null
  }
  key(index: number) {
    return [...this.items.keys()][index] ?? null
  }
  removeItem(key: string) {
    this.items.delete(key)
  }
  setItem(key: string, value: string) {
    this.items.set(key, value)
  }
}

const ticket = (id: number, endsAt: string, status = 'valid'): Ticket =>
  ({
    id,
    code: `T${id}`,
    display_code: `T${id}`,
    status,
    qr_payload: `qr-${id}`,
    event: { id: 1, slug: 'gig', title: 'Gig', starts_at: '2026-12-01T17:00:00Z', ends_at: endsAt, timezone: 'Africa/Johannesburg' },
  }) as Ticket

describe('ticket wallet', () => {
  const now = new Date('2026-11-20T12:00:00Z')

  it('keeps usable tickets for upcoming events for their owner only', () => {
    const storage = new MemoryStorage()
    saveWallet(7, [ticket(1, '2026-12-01T22:00:00Z'), ticket(2, '2026-12-01T22:00:00Z', 'cancelled')], now, storage)
    expect(readWallet(7, now, storage)?.tickets.map((t) => t.id)).toEqual([1])
    expect(readWallet(8, now, storage)).toBeNull()
    // Offline, before the session is known, the last saved wallet is shown.
    expect(readWallet(undefined, now, storage)?.userId).toBe(7)
    // Once the event is over, its tickets drop out.
    expect(readWallet(7, new Date('2026-12-02T00:00:00Z'), storage)?.tickets).toEqual([])
  })

  it('is removed on sign-out and when someone else signs in', () => {
    const storage = new MemoryStorage()
    saveWallet(7, [ticket(1, '2026-12-01T22:00:00Z')], now, storage)
    clearWallet(7, storage)
    expect(readWallet(7, now, storage)).not.toBeNull()
    clearWallet(8, storage)
    expect(readWallet(7, now, storage)).toBeNull()
    saveWallet(7, [ticket(1, '2026-12-01T22:00:00Z')], now, storage)
    clearWallet(undefined, storage)
    expect(readWallet(undefined, now, storage)).toBeNull()
  })

  it('ignores damaged data', () => {
    const storage = new MemoryStorage()
    storage.setItem('gotaticket.wallet', '{not json')
    expect(readWallet(undefined, now, storage)).toBeNull()
  })
})
