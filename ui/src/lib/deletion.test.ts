import {beforeEach, describe, expect, it, vi} from 'vitest'
import {api, isAxiosError} from './axios'
import {fetchDeletionRequest} from './queries'
import {cancelDeletionAPI, confirmDeletionAPI, requestDeletionAPI} from './mutations'

vi.mock('./axios', () => ({
  api: {get: vi.fn(), post: vi.fn()},
  cnpjaApi: {get: vi.fn()},
  isAxiosError: vi.fn(),
}))

describe('deletion API', () => {
  beforeEach(() => vi.clearAllMocks())

  it('returns null when there is no open request (404)', async () => {
    const notFound = {response: {status: 404}}
    vi.mocked(api.get).mockRejectedValue(notFound)
    vi.mocked(isAxiosError).mockReturnValue(true)
    await expect(fetchDeletionRequest()).resolves.toBeNull()
    expect(api.get).toHaveBeenCalledWith('/v1.0/account/deletion')
  })

  it('omits an empty password from the request body', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {request_id: 'r1', state: 'awaiting_confirmation'}})
    await requestDeletionAPI('EXCLUIR MINHA CONTA')
    expect(api.post).toHaveBeenCalledWith('/v1.0/account/deletion', {confirmation_phrase: 'EXCLUIR MINHA CONTA'})
    await requestDeletionAPI('EXCLUIR MINHA CONTA', 'pw')
    expect(api.post).toHaveBeenLastCalledWith('/v1.0/account/deletion', {confirmation_phrase: 'EXCLUIR MINHA CONTA', password: 'pw'})
  })

  it('posts link tokens to the public endpoints', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {request_id: 'r1', state: 'pending_deletion'}})
    await confirmDeletionAPI('r1', 't1')
    expect(api.post).toHaveBeenCalledWith('/v1.0/auth/deletion/confirm', {request_id: 'r1', token: 't1'})
    await cancelDeletionAPI('r1', 't2')
    expect(api.post).toHaveBeenLastCalledWith('/v1.0/auth/deletion/cancel', {request_id: 'r1', token: 't2'})
  })
})
