import { useState } from 'react'
import { Share, Smartphone, SquarePlus } from 'lucide-react'
import { isIosSafari, isStandalone, useInstallPrompt } from '../lib/pwa'
import { appName } from '../lib/brand'
import { Modal } from './ui'

/**
 * "Install the app", shown only where installing is possible: browsers that offer an
 * install prompt, and Safari on iPhone and iPad, which gets the Add to Home Screen steps.
 * Nothing renders inside the installed app itself.
 */
export function InstallApp({ className, onDone }: { className: string; onDone?: () => void }) {
  const { canInstall, install } = useInstallPrompt()
  const [showSteps, setShowSteps] = useState(false)
  const ios = isIosSafari()
  if (isStandalone() || (!canInstall && !ios)) return null

  return (
    <>
      <button
        type="button"
        className={className}
        onClick={async () => {
          if (canInstall) {
            await install()
            onDone?.()
          } else {
            setShowSteps(true)
          }
        }}
      >
        <Smartphone className="size-4 shrink-0" /> Install the app
      </button>
      <Modal
        open={showSteps}
        onClose={() => {
          setShowSteps(false)
          onDone?.()
        }}
        title={`Add ${appName} to your Home Screen`}
        description="Open your tickets in one tap, even without signal."
        size="sm"
      >
        <ol className="space-y-4 text-[15px] text-zinc-700">
          <li className="flex items-start gap-3">
            <span className="grid size-8 shrink-0 place-items-center rounded-full bg-brand-50 text-brand-700"><Share className="size-4" /></span>
            <span>Tap <strong>Share</strong> in Safari's toolbar.</span>
          </li>
          <li className="flex items-start gap-3">
            <span className="grid size-8 shrink-0 place-items-center rounded-full bg-brand-50 text-brand-700"><SquarePlus className="size-4" /></span>
            <span>Scroll down and tap <strong>Add to Home Screen</strong>, then <strong>Add</strong>.</span>
          </li>
        </ol>
      </Modal>
    </>
  )
}
