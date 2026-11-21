import { useEffect, useState } from 'react'
import QR from 'qrcode'
import { Spinner } from './ui'

/** Renders a QR code for value as an image. */
export function QRCode({ value, size = 220, className }: { value: string; size?: number; className?: string }) {
  const [src, setSrc] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    QR.toDataURL(value, { width: size * 2, margin: 1, errorCorrectionLevel: 'M' })
      .then((url) => !cancelled && setSrc(url))
      .catch(() => !cancelled && setSrc(null))
    return () => {
      cancelled = true
    }
  }, [value, size])

  if (!src) {
    return (
      <div className="flex items-center justify-center bg-zinc-50" style={{ width: size, height: size }}>
        <Spinner />
      </div>
    )
  }
  return <img src={src} width={size} height={size} alt="Ticket QR code" className={className} />
}
