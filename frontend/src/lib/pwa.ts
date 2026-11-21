import { useSyncExternalStore } from 'react'

// ---- Install ---------------------------------------------------------------------------

/** The browser's install prompt (Chrome, Edge, Samsung Internet; not Safari or Firefox). */
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

let deferred: BeforeInstallPromptEvent | null = null
const installListeners = new Set<() => void>()
const notifyInstall = () => installListeners.forEach((l) => l())

/**
 * Captures the install prompt. The browser fires it once, early, so this runs when the
 * app starts rather than when a component that shows an install button mounts.
 */
export function listenForInstallPrompt() {
  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    deferred = e as BeforeInstallPromptEvent
    notifyInstall()
  })
  window.addEventListener('appinstalled', () => {
    deferred = null
    notifyInstall()
  })
}

/** Whether this page is running as the installed app rather than in a browser tab. */
export function isStandalone(): boolean {
  return (
    window.matchMedia?.('(display-mode: standalone)').matches ||
    (navigator as Navigator & { standalone?: boolean }).standalone === true
  )
}

/** iPhone and iPad Safari install through Share › Add to Home Screen, with no prompt to show. */
export function isIosSafari(): boolean {
  const ua = navigator.userAgent
  const ios = /iPad|iPhone|iPod/.test(ua) || (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)
  return ios && /Safari/.test(ua) && !/CriOS|FxiOS|EdgiOS/.test(ua)
}

/** The install action, when the browser offers one. */
export function useInstallPrompt(): { canInstall: boolean; install: () => Promise<boolean> } {
  const available = useSyncExternalStore(
    (l) => {
      installListeners.add(l)
      return () => installListeners.delete(l)
    },
    () => deferred !== null,
    () => false,
  )
  return {
    canInstall: available,
    install: async () => {
      const prompt = deferred
      if (!prompt) return false
      // A prompt can only be shown once.
      deferred = null
      notifyInstall()
      await prompt.prompt()
      return (await prompt.userChoice).outcome === 'accepted'
    },
  }
}

// ---- Connection ------------------------------------------------------------------------

/** Whether the browser thinks it has a network connection. */
export function useOnline(): boolean {
  return useSyncExternalStore(
    (l) => {
      window.addEventListener('online', l)
      window.addEventListener('offline', l)
      return () => {
        window.removeEventListener('online', l)
        window.removeEventListener('offline', l)
      }
    },
    () => navigator.onLine,
    () => true,
  )
}
