import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Dialog, DialogContent, DialogTitle } from './dialog'

// A tall form (the new OAuth client dialog with a prefill hint) must scroll
// inside the viewport on a phone instead of being cut off at both ends.
describe('DialogContent', () => {
  it('is bounded by the viewport and scrolls', async () => {
    render(
      <Dialog open>
        <DialogContent>
          <DialogTitle>Title</DialogTitle>
        </DialogContent>
      </Dialog>,
    )
    const content = await screen.findByRole('dialog')
    expect(content.className).toMatch(/max-h-\[calc\(100dvh-2rem\)\]/)
    expect(content.className).toMatch(/overflow-y-auto/)
  })
})
