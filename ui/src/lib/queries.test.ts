import {beforeEach, describe, expect, it, vi} from 'vitest'
import {cnpjaApi} from './axios'
import {lookupTaxID} from './queries'

vi.mock('./axios', () => ({
  api: {},
  cnpjaApi: {get: vi.fn()},
  isAxiosError: vi.fn(),
}))

describe('lookupTaxID', () => {
  beforeEach(() => vi.clearAllMocks())

  it('reads the public CNPJA office response directly', async () => {
    vi.mocked(cnpjaApi.get).mockResolvedValue({
      data: {alias: 'Tartigrado Tecnologia', company: {name: 'TARTIGRADO TECNOLOGIA LTDA'}},
    })

    await expect(lookupTaxID('11.520.224/0001-40')).resolves.toEqual({
      legal_name: 'TARTIGRADO TECNOLOGIA LTDA',
      trade_name: 'Tartigrado Tecnologia',
    })
    expect(cnpjaApi.get).toHaveBeenCalledWith('/office/11520224000140')
  })

  it('does not send a CPF to the CNPJ-only register', async () => {
    await expect(lookupTaxID('111.444.777-35')).resolves.toBeNull()
    expect(cnpjaApi.get).not.toHaveBeenCalled()
  })
})
