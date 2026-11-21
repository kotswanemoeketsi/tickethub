// Marketing and help pages: selling with us, the help centre and the terms of sale.
// The text describes how the platform actually behaves; keep it in step with the code.
import type { ReactNode } from 'react'
import { Link } from 'react-router'
import { BarChart3, CreditCard, Landmark, QrCode, Ticket, Users } from 'lucide-react'
import { appName } from '../../layouts/PublicLayout'

function PageHero({ title, intro }: { title: string; intro: string }) {
  return (
    <section className="bg-ink-900 text-white">
      <div className="mx-auto max-w-4xl px-4 py-14 sm:px-6 sm:py-20">
        <h1 className="text-4xl font-bold sm:text-5xl">{title}</h1>
        <p className="mt-4 max-w-2xl text-lg text-ink-200">{intro}</p>
      </div>
    </section>
  )
}

export function SellPage() {
  const features = [
    { icon: <Ticket className="size-5" />, title: 'Ticket types your way', text: 'General, VIP, early bird and group tickets, each with its own price, quantity, sale window and order limits. Promo codes and complimentary tickets included.' },
    { icon: <CreditCard className="size-5" />, title: 'Online and EFT payments', text: 'Buyers pay by card or instant EFT, or reserve and pay by bank transfer. Tickets are only issued once payment is verified.' },
    { icon: <QrCode className="size-5" />, title: 'Scan at the door', text: 'Every ticket carries a signed QR code. Your door staff scan with any phone; copies, refunded and already-used tickets are refused.' },
    { icon: <BarChart3 className="size-5" />, title: 'Live reports', text: 'Sales, revenue, attendance and payouts as they happen, exportable to Excel, CSV or PDF.' },
    { icon: <Users className="size-5" />, title: 'Your whole team', text: 'Invite colleagues with the access they need: event managers, finance, door staff. They see only your events.' },
    { icon: <Landmark className="size-5" />, title: 'Simple pricing', text: 'No set-up or monthly fees. A small commission per ticket sold, agreed when your account is approved.' },
  ]
  const steps = [
    ['Apply', 'Tell us about your organisation. We review every application and email you as soon as your account is approved.'],
    ['Create your event', 'Add the details, artwork, venue and ticket types, then submit it for a quick review.'],
    ['Sell and scan', 'Once approved your event is live. Share the link, watch sales come in and scan tickets at the door.'],
  ]
  return (
    <>
      <PageHero title="Sell tickets with us" intro={`${appName} gives event organisers everything needed to sell tickets and run the door, from a single event to a full season.`} />
      <section className="mx-auto max-w-6xl px-4 py-14 sm:px-6">
        <div className="grid gap-x-10 gap-y-10 sm:grid-cols-2 lg:grid-cols-3">
          {features.map((f) => (
            <div key={f.title}>
              <div className="flex size-11 items-center justify-center rounded-xl bg-brand-50 text-brand-700">{f.icon}</div>
              <h2 className="mt-4 font-sans text-lg font-semibold text-ink-900">{f.title}</h2>
              <p className="mt-1.5 text-[15px] leading-relaxed text-zinc-600">{f.text}</p>
            </div>
          ))}
        </div>
      </section>
      <section className="mx-auto max-w-6xl px-4 pb-6 sm:px-6">
        <div className="rounded-3xl bg-white p-8 ring-1 ring-zinc-200 sm:p-12">
          <h2 className="text-3xl font-bold text-ink-900">Getting started</h2>
          <ol className="mt-8 grid gap-8 md:grid-cols-3">
            {steps.map(([title, text], i) => (
              <li key={title}>
                <span className="font-display text-4xl font-bold text-brand-600">{i + 1}</span>
                <h3 className="mt-2 font-sans text-lg font-semibold text-ink-900">{title}</h3>
                <p className="mt-1 text-[15px] text-zinc-600">{text}</p>
              </li>
            ))}
          </ol>
          <Link to="/organisers/apply" className="mt-10 inline-block rounded-full bg-ink-900 px-7 py-3.5 font-semibold text-white hover:bg-ink-800">
            Apply as an organiser
          </Link>
        </div>
      </section>
    </>
  )
}

function Faq({ q, children }: { q: string; children: ReactNode }) {
  return (
    <details className="group border-b border-zinc-200 py-5">
      <summary className="flex cursor-pointer list-none items-center justify-between gap-4 text-[17px] font-semibold text-ink-900">
        {q}
        <span className="text-2xl font-light text-brand-600 transition-transform group-open:rotate-45" aria-hidden>+</span>
      </summary>
      <div className="mt-3 max-w-[68ch] text-[15px] leading-relaxed text-zinc-600">{children}</div>
    </details>
  )
}

export function HelpPage() {
  return (
    <>
      <PageHero title="Help centre" intro="Answers to the questions we hear most. If yours isn't here, get in touch." />
      <div className="mx-auto max-w-3xl px-4 py-12 sm:px-6">
        <h2 id="tickets" className="scroll-mt-40 text-2xl font-bold text-ink-900">Buying and using tickets</h2>
        <Faq q="Where are my tickets?">
          Each ticket is emailed as a PDF to the person it was issued to, and every ticket you bought is in <Link to="/account/tickets" className="font-medium text-brand-700">My tickets</Link>. Show the QR code on your phone or on paper at the door.
        </Faq>
        <Faq q="How long are tickets held while I check out?">
          Your tickets are reserved for 15 minutes from when you choose them. Once you start paying online the hold is extended so a slow payment page does not cost you your seats.
        </Faq>
        <Faq q="Can I pay by EFT?">
          Yes, for events that are not starting soon. Choose manual EFT at checkout, pay into the account shown using your order reference, and your tickets are issued once the payment reflects. The order shows a pay-by time; unpaid orders are released after it.
        </Faq>
        <Faq q="Can I give a ticket to someone else?">
          Yes. Open the order and choose “Change holder”. The ticket is reissued with a new QR code and emailed to the new holder, and the old QR code stops working.
        </Faq>
        <Faq q="My QR code was refused at the door.">
          The scanner shows door staff why: for example the ticket is for another event, was refunded, was already used, or was replaced when it was transferred. Open the ticket in your account to make sure you are showing the latest one.
        </Faq>

        <h2 id="refunds" className="mt-12 scroll-mt-40 text-2xl font-bold text-ink-900">Refunds</h2>
        <Faq q="Can I get a refund?">
          It depends on the event. Each event page states whether refunds are offered and until when. If they are, request one from your order page; once approved, card and instant EFT payments go back to the original payment method and EFT payments are paid back to you by bank transfer.
        </Faq>
        <Faq q="What happens if an event is cancelled?">
          Every ticket holder is emailed and every paid order is refunded in full. You do not need to do anything.
        </Faq>

        <h2 id="contact" className="mt-12 scroll-mt-40 text-2xl font-bold text-ink-900">Contact us</h2>
        <p className="mt-3 text-[15px] text-zinc-600">
          For questions about an order, reply to your ticket email or write to us with your order reference. For questions about an event itself, such as age limits or parking, contact the organiser named on the event page.
        </p>
      </div>
    </>
  )
}

export function TermsPage() {
  const section = (title: string, body: ReactNode) => (
    <section className="mt-10">
      <h2 className="text-xl font-bold text-ink-900">{title}</h2>
      <div className="mt-2 space-y-3 text-[15px] leading-relaxed text-zinc-600">{body}</div>
    </section>
  )
  return (
    <>
      <PageHero title="Terms of sale" intro="A plain-language summary of how buying tickets on this platform works." />
      <div className="mx-auto max-w-3xl px-4 py-6 sm:px-6">
        {section('Who you are buying from', <p>Events are run by the organisers named on each event page. {appName} sells tickets on their behalf and processes the payment.</p>)}
        {section('Reservations and payment', <p>Tickets you select are held for you for a limited time while you check out. A ticket is only yours once payment has been verified; unpaid reservations are released automatically.</p>)}
        {section('Tickets', <p>Each ticket admits one person and carries a unique QR code. Only the most recent version of a ticket is valid; transferring a ticket replaces its QR code. Keep your tickets private, as a copied QR code can only be used once.</p>)}
        {section('Refunds and cancellations', <p>Refunds are available only where the event page says so, up to the stated cut-off. If an event is cancelled, all paid orders are refunded in full.</p>)}
        {section('Entry', <p>Organisers and venues may set their own entry conditions. Re-entry is only possible at events that allow it.</p>)}
      </div>
    </>
  )
}
