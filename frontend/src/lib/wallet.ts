// The offline ticket wallet: a copy of the signed-in user's upcoming tickets kept on this
// device, so they can be shown at the door without a connection. It belongs to one user,
// is replaced every time the tickets load online, and is deleted when that user signs out
// or another user signs in.

import type { Ticket } from './types'

const storageKey = 'gotaticket.wallet'

export interface Wallet {
  userId: number
  savedAt: string
  tickets: Ticket[]
}

/** localStorage when the browser allows it; private modes and old browsers may not. */
function defaultStorage(): Storage | null {
  try {
    return globalThis.localStorage ?? null
  } catch {
    return null
  }
}

/** Saves the user's upcoming tickets, keeping only those that can still be used. */
export function saveWallet(userId: number, tickets: Ticket[], now = new Date(), storage = defaultStorage()) {
  if (!storage) return
  const wallet: Wallet = { userId, savedAt: now.toISOString(), tickets: tickets.filter((t) => t.status === 'valid' && t.qr_payload) }
  try {
    storage.setItem(storageKey, JSON.stringify(wallet))
  } catch {
    // Full or blocked storage only costs the offline copy; the page still works online.
  }
}

/**
 * Returns the saved tickets whose events haven't finished. With userId, only that user's
 * wallet is returned; without it (offline, before the session can be checked), whichever
 * user last saved tickets on this device.
 */
export function readWallet(userId?: number, now = new Date(), storage = defaultStorage()): Wallet | null {
  if (!storage) return null
  try {
    const raw = storage.getItem(storageKey)
    if (!raw) return null
    const wallet = JSON.parse(raw) as Wallet
    if (!Array.isArray(wallet.tickets) || typeof wallet.userId !== 'number') return null
    if (userId !== undefined && wallet.userId !== userId) return null
    const tickets = wallet.tickets.filter((t) => !t.event || new Date(t.event.ends_at) > now)
    return { ...wallet, tickets }
  } catch {
    return null
  }
}

/** Deletes the wallet, or only another user's wallet when keepUserId is given. */
export function clearWallet(keepUserId?: number, storage = defaultStorage()) {
  if (!storage) return
  try {
    if (keepUserId !== undefined) {
      const raw = storage.getItem(storageKey)
      if (raw && (JSON.parse(raw) as Wallet).userId === keepUserId) return
    }
    storage.removeItem(storageKey)
  } catch {
    // Nothing to clear, or storage is unavailable.
  }
}
