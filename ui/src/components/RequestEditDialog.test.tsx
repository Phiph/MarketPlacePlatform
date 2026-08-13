import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { RequestEditDialog } from './RequestEditDialog'
import type { JsonSchema, ResourceRequest } from '@/lib/types'

const schema: JsonSchema = {
  type: 'object',
  properties: {
    size: { type: 'string', description: 'Storage volume size.' },
  },
}

function makeRequest(spec: Record<string, unknown>): ResourceRequest {
  return {
    apiVersion: 'demo.kratix.io/v1alpha1',
    kind: 'Database',
    metadata: { name: 'my-db', namespace: 'team-payments' },
    spec,
  }
}

// This dialog is the UI half of the request-editing feature (see
// broker/internal/api's PUT routes) - it's what turns a user's form edits
// into the spec onSave receives. Real API-contract behaviour (status
// codes, response shapes) is covered separately in api.integration.test.ts
// against the real broker; this is purely the component's own rendering
// and interaction logic, so a mocked onSave is the right boundary here.

describe('RequestEditDialog', () => {
  it('prefills the form from the request being edited', () => {
    render(
      <RequestEditDialog request={makeRequest({ size: '10Gi' })} schema={schema} onOpenChange={vi.fn()} onSave={vi.fn()} />,
    )

    expect(screen.getByLabelText('size')).toHaveValue('10Gi')
    expect(screen.getByRole('heading', { name: 'my-db' })).toBeInTheDocument()
  })

  it('saves the edited spec, not the original', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const onOpenChange = vi.fn()

    render(<RequestEditDialog request={makeRequest({ size: '10Gi' })} schema={schema} onOpenChange={onOpenChange} onSave={onSave} />)

    const input = screen.getByLabelText('size')
    await user.clear(input)
    await user.type(input, '50Gi')
    await user.click(screen.getByRole('button', { name: /save changes/i }))

    expect(onSave).toHaveBeenCalledWith({ size: '50Gi' })
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('closes only after onSave resolves, and disables the button meanwhile', async () => {
    const user = userEvent.setup()
    let resolveSave: () => void = () => {}
    const onSave = vi.fn(() => new Promise<void>((resolve) => { resolveSave = resolve }))
    const onOpenChange = vi.fn()

    render(<RequestEditDialog request={makeRequest({ size: '10Gi' })} schema={schema} onOpenChange={onOpenChange} onSave={onSave} />)

    const saveButton = screen.getByRole('button', { name: /save changes/i })
    await user.click(saveButton)

    expect(saveButton).toBeDisabled()
    expect(onOpenChange).not.toHaveBeenCalled()

    resolveSave()
    await vi.waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })
})
