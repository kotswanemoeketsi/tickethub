import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { RotateCcw, Sparkles } from 'lucide-react'
import { api, errorMessage, fieldErrors } from '../lib/api'
import type { EventDraft } from '../lib/ai'
import type { Category } from '../lib/types'
import { Alert, Button, Modal, Textarea } from './ui'

/**
 * "Write with AI": the organiser describes the event in a few lines and gets a title,
 * summary, description and category to review. Nothing changes until they choose
 * "Use this text", and the form still has to be saved.
 */
export function EventWriter({ organiserId, currentTitle, categories, hasText, onUse }: {
  organiserId: string
  currentTitle: string
  categories: Category[]
  hasText: boolean
  onUse: (draft: EventDraft, categoryId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [notes, setNotes] = useState('')
  const write = useMutation({
    mutationFn: () => api<EventDraft>('/admin/ai/event-draft', { method: 'POST', body: { organiser_id: Number(organiserId), notes, title: currentTitle } }),
  })
  const draft = write.data
  const category = categories.find((c) => c.slug === draft?.category)
  const errors = fieldErrors(write.error)
  const close = () => {
    setOpen(false)
    write.reset()
  }

  return (
    <>
      <Button type="button" variant="secondary" size="sm" icon={<Sparkles className="size-4" />} onClick={() => setOpen(true)} disabled={!organiserId}>
        Write with AI
      </Button>
      <Modal
        open={open}
        onClose={close}
        size="lg"
        title="Write with AI"
        description="Describe your event in your own words. You'll get a title, summary and description to review and edit."
        footer={
          draft ? (
            <>
              <Button type="button" variant="secondary" icon={<RotateCcw className="size-4" />} loading={write.isPending} onClick={() => write.mutate()} className="mr-auto">
                Write again
              </Button>
              <Button type="button" variant="secondary" onClick={close}>Cancel</Button>
              <Button
                type="button"
                onClick={() => {
                  onUse(draft, category ? String(category.id) : '')
                  close()
                }}
              >
                Use this text
              </Button>
            </>
          ) : (
            <>
              <Button type="button" variant="secondary" onClick={close}>Cancel</Button>
              <Button type="button" icon={<Sparkles className="size-4" />} loading={write.isPending} onClick={() => write.mutate()}>Write</Button>
            </>
          )
        }
      >
        {!draft ? (
          <div className="space-y-4">
            {write.error && !Object.keys(errors).length && <Alert tone="error">{errorMessage(write.error)}</Alert>}
            <Textarea
              label="About your event"
              rows={7}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              error={errors.notes}
              maxLength={3000}
              placeholder="For example: an evening of stand-up with five Durban comics, hosted by Thando. Outdoor seating, food trucks from 17:00, bring a blanket."
              hint="Mention what happens, who it's for and what makes it special. Leave out phone numbers and email addresses: they are removed before your notes are sent."
            />
          </div>
        ) : (
          <div className="space-y-5 text-[15px]">
            {hasText && <Alert tone="warning">Using this text replaces the title, summary and description already in the form.</Alert>}
            <div>
              <p className="text-sm font-medium text-zinc-500">Title</p>
              <p className="mt-1 font-display text-xl font-semibold text-ink-900">{draft.title}</p>
            </div>
            <div>
              <p className="text-sm font-medium text-zinc-500">Summary</p>
              <p className="mt-1 text-zinc-800">{draft.summary}</p>
            </div>
            <div>
              <p className="text-sm font-medium text-zinc-500">Description</p>
              <p className="prose-text mt-1 max-h-72 overflow-y-auto rounded-xl bg-zinc-50 p-4 text-zinc-800">{draft.description}</p>
            </div>
            {category && (
              <div>
                <p className="text-sm font-medium text-zinc-500">Category</p>
                <p className="mt-1 text-zinc-800">{category.name}</p>
              </div>
            )}
            {!!draft.to_add?.length && (
              <div className="rounded-xl bg-brand-50 p-4">
                <p className="text-sm font-semibold text-brand-800">Buyers may also want to know</p>
                <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-brand-900">
                  {draft.to_add.map((q) => <li key={q}>{q}</li>)}
                </ul>
              </div>
            )}
            <p className="text-xs text-zinc-500">Written by AI from your notes. Check every detail before you save: you are responsible for what the listing says.</p>
          </div>
        )}
      </Modal>
    </>
  )
}
