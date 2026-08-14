import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { RequestVersionDialog } from './RequestVersionDialog'
import { ApiError } from '@/lib/api'
import type { PromiseRevision, RequestVersionInfo, ResourceRequest } from '@/lib/types'

const request: ResourceRequest = {
  apiVersion: 'demo.kratix.io/v1alpha1',
  kind: 'Database',
  metadata: { name: 'my-db', namespace: 'team-payments' },
  spec: { size: '1Gi' },
}

const versionInfo: RequestVersionInfo = { boundVersion: 'v0.1.0', latestVersion: 'v0.2.0', upgradeAvailable: true }
const versions: PromiseRevision[] = [
  { version: 'v0.1.0', latest: false, createdAt: '2026-07-01T00:00:00Z' },
  { version: 'v0.2.0', latest: true, createdAt: '2026-08-10T00:00:00Z' },
]

describe('RequestVersionDialog', () => {
  it('shows a loading state while versions are still being fetched', () => {
    render(
      <RequestVersionDialog
        request={request}
        versionInfo={versionInfo}
        versions={undefined}
        onOpenChange={vi.fn()}
        onSetVersion={vi.fn()}
      />,
    )

    expect(screen.getByText(/loading versions/i)).toBeInTheDocument()
  })

  it('moves to the selected version and closes on success', async () => {
    const user = userEvent.setup()
    const onSetVersion = vi.fn().mockResolvedValue(undefined)
    const onOpenChange = vi.fn()

    render(
      <RequestVersionDialog
        request={request}
        versionInfo={versionInfo}
        versions={versions}
        onOpenChange={onOpenChange}
        onSetVersion={onSetVersion}
      />,
    )

    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('option', { name: /v0\.2\.0/i }))
    await user.click(screen.getByRole('button', { name: /move to this version/i }))

    expect(onSetVersion).toHaveBeenCalledWith('v0.2.0')
    await vi.waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('disables the move button when the selected version is already the bound one', () => {
    render(
      <RequestVersionDialog
        request={request}
        versionInfo={versionInfo}
        versions={versions}
        onOpenChange={vi.fn()}
        onSetVersion={vi.fn()}
      />,
    )

    // target seeds from versionInfo.boundVersion (v0.1.0) - moving to the
    // same version the request is already on should be a no-op, not a call.
    expect(screen.getByRole('button', { name: /move to this version/i })).toBeDisabled()
  })

  it('stays open and shows the API error message when the move is rejected', async () => {
    const user = userEvent.setup()
    const onSetVersion = vi.fn().mockRejectedValue(new ApiError(400, 'spec is not valid for version v0.2.0: missing required field "size"'))
    const onOpenChange = vi.fn()

    render(
      <RequestVersionDialog
        request={request}
        versionInfo={versionInfo}
        versions={versions}
        onOpenChange={onOpenChange}
        onSetVersion={onSetVersion}
      />,
    )

    await user.click(screen.getByRole('combobox'))
    await user.click(screen.getByRole('option', { name: /v0\.2\.0/i }))
    await user.click(screen.getByRole('button', { name: /move to this version/i }))

    await vi.waitFor(() => expect(onSetVersion).toHaveBeenCalled())
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})
