import {beforeEach, describe, expect, it, vi} from 'vitest'
import {api} from './axios'
import {fetchSpaces} from './queries'
import {createOrganizationAPI} from './mutations'
import {assignableRoles, canTransferTo, isPersonal} from './types'
import {workspaceKey} from './workspace-copy'
import i18n from './i18n'
import ptBR from '@/locales/pt-BR.json'
import en from '@/locales/en.json'

vi.mock('./axios', () => ({
  api: {get: vi.fn(), post: vi.fn()},
  cnpjaApi: {get: vi.fn()},
  isAxiosError: vi.fn(),
}))

describe('workspace kind', () => {
  beforeEach(() => vi.clearAllMocks())

  // An absent kind is an organization: every row written before spaces existed
  // has none, and nothing is migrated.
  it('reads an absent kind as an organization', () => {
    expect(isPersonal({kind: undefined})).toBe(false)
    expect(isPersonal({kind: 'organization'})).toBe(false)
    expect(isPersonal({kind: 'personal'})).toBe(true)
  })

  // A space has two levels and only its owner hands them out. Offering admin
  // would be offering a choice the server answers with 422.
  it('lets only the owner of a space grant, and never admin', () => {
    expect(assignableRoles('owner', 'personal')).toEqual(['member', 'viewer'])
    expect(assignableRoles('member', 'personal')).toEqual([])
    expect(assignableRoles('viewer', 'personal')).toEqual([])
  })

  it('leaves the organization ladder exactly as it was', () => {
    expect(assignableRoles('owner')).toEqual(['admin', 'member', 'viewer'])
    expect(assignableRoles('admin', 'organization')).toEqual(['member', 'viewer'])
    expect(assignableRoles('member')).toEqual([])
  })

  // A viewer is promoted first; handing a space to somebody who can only read
  // it is refused by the server.
  it('transfers a space only to somebody with full access', () => {
    expect(canTransferTo('personal', 'member')).toBe(true)
    expect(canTransferTo('personal', 'viewer')).toBe(false)
    expect(canTransferTo('personal', 'owner')).toBe(false)
    expect(canTransferTo(undefined, 'viewer')).toBe(true)
    expect(canTransferTo('organization', 'owner')).toBe(false)
  })

  it('lists spaces through the kind filter', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {organizations: [{id: 'org_s', display_name: 'Casa', kind: 'personal'}]},
    })
    await expect(fetchSpaces()).resolves.toEqual([
      {id: 'org_s', display_name: 'Casa', kind: 'personal'},
    ])
    expect(api.get).toHaveBeenCalledWith('/v1.0/organizations', {params: {kind: 'personal'}})
  })

  it('creates a space with the personal kind', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {id: 'org_s'}})
    await createOrganizationAPI('Casa', 'personal')
    expect(api.post).toHaveBeenCalledWith('/v1.0/organizations', {
      display_name: 'Casa',
      kind: 'personal',
    })
  })

  // The organization paths never send a kind: the server's default is the
  // organization, and the organization handoff must never create a space.
  it('sends no kind when creating an organization', async () => {
    vi.mocked(api.post).mockResolvedValue({data: {id: 'org_o'}})
    await createOrganizationAPI('CTech')
    expect(api.post).toHaveBeenCalledWith('/v1.0/organizations', {display_name: 'CTech'})
  })
})

describe('space copy', () => {
  const exists = (key: string) => i18n.exists(key)

  it('swaps in the space wording where there is one', () => {
    expect(workspaceKey('organizations.settings.transfer', 'personal', exists)).toBe(
      'spaces.settings.transfer',
    )
    expect(workspaceKey('toast.transferFailed', 'personal', exists)).toBe(
      'spaces.toast.transferFailed',
    )
  })

  it('falls back to the shared wording where nothing differs', () => {
    expect(workspaceKey('organizations.members.user', 'personal', exists)).toBe(
      'organizations.members.user',
    )
  })

  it('never touches an organization', () => {
    expect(workspaceKey('organizations.settings.transfer', 'organization', exists)).toBe(
      'organizations.settings.transfer',
    )
    expect(workspaceKey('organizations.settings.transfer', undefined, exists)).toBe(
      'organizations.settings.transfer',
    )
  })

  // A space has none of an organization's vocabulary. A string that slips one
  // in tells a couple sharing a grocery budget they founded a company.
  it('never says organization or company in the space wording', () => {
    const strings = (node: unknown): string[] =>
      typeof node === 'string'
        ? [node]
        : Object.values(node as Record<string, unknown>).flatMap(strings)
    for (const text of strings(ptBR.spaces)) {
      expect(text).not.toMatch(/organiza|empresa|cnpj/i)
    }
    for (const text of strings(en.spaces)) {
      expect(text).not.toMatch(/organi[sz]ation|company|cnpj/i)
    }
  })
})
