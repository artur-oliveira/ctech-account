# Account Deletion — Phase 4 (UI) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give users the account-deletion flow in `accounts.aoctech.app`:
- a "Delete account" entry in Profile, leading to a page that explains the consequences, shows blockers and submits the request;
- the two pages the e-mail links open: confirm, and cancel;
- login messages for a pending account;
- a new Privacy Policy version that describes deletion and retention.

**Architecture:**
- Client Components only (static export).
- All calls go through `lib/queries.ts` / `lib/mutations.ts` and the shared `api` instance.
- Copy lives in `locales/en.json` and `locales/pt-BR.json` under `deletion.*`.
- The e-mail-link pages act **only on a button click** (POST), never on page load, so e-mail scanners that prefetch links cannot confirm or cancel anything.

**Tech Stack:** Next.js 16 (static export), React 19, ShadCN 4 (@base-ui), TanStack Query, react-i18next, Vitest + RTL.

**Spec:** `docs/specs/2026-10-06-account-deletion-ctech-account.md` §8; API contract in `api/ENDPOINTS.md` "Account deletion" (Phases 1–3).

**Builds on:** API Phases 1–3. The UI can ship once Phase 2b is live: it only needs the Phase 1–2 endpoints. `legal_hold` (Phase 3) is optional in the response type.

## Global Constraints

- Branch `feat/account-deletion-ui`. Conventional Commits, **no `Co-Authored-By` / Claude attribution**.
- `ui/CLAUDE.md` rules:
  - ShadCN 4 `render` prop (no `asChild`);
  - `useSearchParams` inside `<Suspense>`;
  - no derived state copied via `useEffect`;
  - types in `lib/types.ts` with backend field names;
  - constants in `lib/constants.ts`.
- Typed phrase: `EXCLUIR MINHA CONTA` (the API compares it exactly; show it verbatim in both languages).
- Problem types matched by suffix (as `login/mfa` does): `deletion-blocked`, `account-pending-deletion`, `conflict`, `invalid-token`, `invalid-credentials`, `too-many-requests`, `service-unavailable`.
- Before every commit: `cd ui && npx eslint src --ext .ts,.tsx && npm test && npm run build` → clean.

## Rulings

- **R14 The password field is shown when `profile.has_password`, and is then required.** The API accepts a recent MFA proof instead, but always asking keeps the page from depending on token freshness. It also never triggers the global step-up dialog, which would push MFA-less users to enrol. Cost if wrong: an MFA user types a password they could have skipped.
- **R15 Privacy Policy 3.3 needs legal sign-off.** Task 6 prepares it. Bumping `CurrentPrivacyVersion` makes every user re-accept, through the existing terms gate. **Do not merge Task 6 until the user confirms the text was approved.**

## Review Focus

1. **An e-mail scanner opens the confirm link.** Nothing happens until the user presses the button. Test: Task 4 `TestConfirmPage_DoesNothingUntilClicked`.
2. **The request is refused with blockers.** Each blocker is shown in plain language, with its action link when the participant provided one. Unknown codes fall back to a generic line instead of a crash or a raw code. Test: Task 3 `DeleteAccountPage shows blockers`.
3. **A cancel link used after the grace period** says the deletion can no longer be cancelled (409), not "invalid link". Test: Task 5 `CancelPage distinguishes too-late from invalid`.
4. **Login of a pending account** (password, or the Google redirect) explains the account is scheduled for deletion and where to cancel. Test: Task 5 `login shows pending-deletion message`.
5. **A user with no password (Google-only)** sees no password field and can submit. Test: Task 3 `DeleteAccountPage without password`.

---

### Task 1: Types, constants, queries and mutations

**Files:** Modify `ui/src/lib/types.ts`, `ui/src/lib/constants.ts`, `ui/src/lib/queries.ts`, `ui/src/lib/mutations.ts`; Create `ui/src/lib/deletion.test.ts`.

**Interfaces:**
- Produces:
  - `DeletionState`, `DeletionRequest`, `DeletionBlocker`;
  - `ProblemDetail.blockers?: DeletionBlocker[]`;
  - constants `DELETION_PHRASE`, `DELETION_BLOCKED_PROBLEM`, `ACCOUNT_PENDING_DELETION_PROBLEM`, `CONFLICT_PROBLEM`, `INVALID_TOKEN_PROBLEM`;
  - `fetchDeletionRequest(): Promise<DeletionRequest | null>`;
  - `requestDeletionAPI(confirmationPhrase: string, password?: string): Promise<DeletionRequest>`;
  - `confirmDeletionAPI(requestID: string, token: string): Promise<DeletionRequest>`;
  - `cancelDeletionAPI(requestID: string, token: string): Promise<DeletionRequest>`.

- [ ] **Step 1: Failing test** — `ui/src/lib/deletion.test.ts`:

```ts
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
```

- [ ] **Step 2: Fail** — `cd ui && npx vitest run src/lib/deletion.test.ts` → fails (exports missing).

- [ ] **Step 3: Implement.**

`lib/types.ts` — extend `ProblemDetail` with `blockers?: DeletionBlocker[]` and add:

```ts
export type DeletionState =
  | 'awaiting_confirmation' | 'pending_deletion' | 'blocked' | 'locked'
  | 'purging' | 'stalled' | 'purged' | 'cancelled' | 'expired'

export type DeletionRequest = {
  request_id: string
  state: DeletionState
  requested_at?: string
  confirm_by?: string
  grace_until?: string
  legal_hold?: boolean
}

export type DeletionBlocker = {
  code: string
  detail?: Record<string, unknown>
  action_url?: string
}
```

`lib/constants.ts`:

```ts
// Account deletion (docs/specs/2026-10-06-account-deletion-ctech-account.md).
// The API compares the phrase exactly; it is shown verbatim in every language.
export const DELETION_PHRASE = 'EXCLUIR MINHA CONTA'
export const DELETION_BLOCKED_PROBLEM = 'deletion-blocked'
export const ACCOUNT_PENDING_DELETION_PROBLEM = 'account-pending-deletion'
export const CONFLICT_PROBLEM = 'conflict'
export const INVALID_TOKEN_PROBLEM = 'invalid-token'
```

`lib/queries.ts`:

```ts
export async function fetchDeletionRequest(): Promise<DeletionRequest | null> {
  try {
    const {data} = await api.get<DeletionRequest>('/v1.0/account/deletion')
    return data
  } catch (err) {
    if (isAxiosError(err) && err.response?.status === 404) return null
    throw err
  }
}
```

(import `isAxiosError` from `./axios` and `DeletionRequest` from `./types` if not already imported).

`lib/mutations.ts`:

```ts
export async function requestDeletionAPI(confirmationPhrase: string, password?: string): Promise<DeletionRequest> {
  const body: Record<string, string> = {confirmation_phrase: confirmationPhrase}
  if (password) body.password = password
  const {data} = await api.post<DeletionRequest>('/v1.0/account/deletion', body)
  return data
}

export async function confirmDeletionAPI(requestID: string, token: string): Promise<DeletionRequest> {
  const {data} = await api.post<DeletionRequest>('/v1.0/auth/deletion/confirm', {request_id: requestID, token})
  return data
}

export async function cancelDeletionAPI(requestID: string, token: string): Promise<DeletionRequest> {
  const {data} = await api.post<DeletionRequest>('/v1.0/auth/deletion/cancel', {request_id: requestID, token})
  return data
}
```

- [ ] **Step 4: Pass** — `npx vitest run src/lib/deletion.test.ts` → pass.
- [ ] **Step 5: Commit** — `git add ui/src/lib && git commit -m "feat(ui): account deletion API client"`

---

### Task 2: Copy (en, pt-BR)

**Files:** Modify `ui/src/locales/en.json`, `ui/src/locales/pt-BR.json`.

- [ ] **Step 1:** Add a top-level `"deletion"` object to `pt-BR.json`:

```json
  "deletion": {
    "cardTitle": "Excluir conta",
    "cardDesc": "Apaga sua conta CTech e seus dados em todos os produtos CTech.",
    "cardAction": "Excluir minha conta",
    "title": "Excluir conta",
    "subtitle": "Leia com atenção: depois do prazo de 7 dias, a exclusão não pode ser desfeita.",
    "whatHappens": "O que acontece",
    "steps": [
      "Enviamos um link de confirmação para o seu e-mail (válido por 24 horas).",
      "Ao confirmar, sua conta é bloqueada na hora em todos os produtos CTech.",
      "Durante 7 dias você pode cancelar pelo link que enviaremos por e-mail.",
      "Depois disso, seus dados são apagados ou anonimizados em todos os produtos."
    ],
    "whatIsKept": "O que a lei nos obriga a manter",
    "kept": [
      "Registros de verificação de identidade (KYC) e movimentações financeiras do CTech Ledger, por 5 anos.",
      "Documentos fiscais de organizações que continuam ativas com outros membros.",
      "Um código irreversível derivado do seu CPF, para prevenção a fraudes."
    ],
    "soleOrgsWarning": "Organizações em que você é o único membro serão apagadas junto, com todas as notas fiscais. Exporte os XMLs antes.",
    "phraseLabel": "Para confirmar, digite {{phrase}}",
    "passwordLabel": "Senha da conta",
    "noPasswordHint": "Sua conta não tem senha: a confirmação será feita pelo seu e-mail.",
    "submit": "Solicitar exclusão",
    "checkEmail": "Enviamos um link de confirmação para o seu e-mail. Ele vale até {{date}}.",
    "awaitingConfirmation": "Há um pedido aguardando confirmação no seu e-mail até {{date}}. Você pode pedir um novo link enviando o formulário novamente.",
    "legalHold": "Sua solicitação está suspensa por bloqueio jurídico. Fale com o suporte para mais informações.",
    "blockedTitle": "Antes de excluir a conta, resolva:",
    "blockers": {
      "account.organization_shared_owner": "Você é titular de uma organização com outros membros. Transfira a titularidade.",
      "account.oauth_client_owned": "Você tem aplicativos OAuth cadastrados. Exclua-os em Aplicativos OAuth.",
      "wallet.balance_nonzero": "Você tem saldo no CTech Ledger. Use o saldo antes de excluir a conta.",
      "poker.seated_at_table": "Você está sentado em uma mesa do CTech Poker. Saia da mesa.",
      "poker.chips_held": "Você tem fichas em jogo no CTech Poker. Encerre a partida.",
      "poker.pending_cashout": "Há um resgate do CTech Poker em processamento. Aguarde a conclusão.",
      "poker.pending_fee_debit": "Há uma cobrança do CTech Poker em processamento. Aguarde a conclusão.",
      "billing.invoice_open": "Você tem uma fatura da CTech em aberto. Pague-a antes de excluir a conta.",
      "billing.tenant_owner": "Você é responsável por uma conta de cobrança. Fale com o suporte.",
      "unknown": "Um produto CTech informou uma pendência ({{code}}). Fale com o suporte."
    },
    "blockerAction": "Resolver",
    "errors": {
      "phrase": "Digite a frase exatamente como mostrada.",
      "password": "Senha incorreta.",
      "conflict": "Sua conta já tem uma exclusão em andamento. Verifique seu e-mail.",
      "tooMany": "Muitas tentativas. Tente novamente mais tarde.",
      "unavailable": "Não conseguimos verificar todos os produtos CTech agora. Tente novamente em alguns minutos."
    },
    "confirm": {
      "title": "Confirmar exclusão da conta",
      "description": "Ao confirmar, sua conta é bloqueada imediatamente e será excluída em 7 dias.",
      "button": "Confirmar exclusão",
      "done": "Exclusão confirmada. Sua conta será excluída em {{date}}. Enviamos para o seu e-mail um link para cancelar até lá.",
      "invalid": "Este link é inválido ou expirou. Faça um novo pedido em Perfil › Excluir conta."
    },
    "cancel": {
      "title": "Cancelar exclusão da conta",
      "description": "Sua conta volta a funcionar. Por segurança, você precisará entrar de novo.",
      "button": "Cancelar exclusão",
      "done": "Exclusão cancelada. Sua conta foi mantida.",
      "tooLate": "O prazo terminou e a exclusão não pode mais ser cancelada.",
      "invalid": "Este link é inválido."
    },
    "goToLogin": "Ir para o login"
  },
```

Add the same keys to `en.json` with English text (same structure; e.g. `"title": "Delete account"`, `"submit": "Request deletion"`, `"confirm.button": "Confirm deletion"`, `"cancel.button": "Cancel deletion"`). Keep `{{phrase}}`, `{{date}}` and `{{code}}`.

Under `errors` in both files, add:
- `"accountPendingDeletion"`: "Esta conta está agendada para exclusão. Para mantê-la, use o link de cancelamento enviado ao seu e-mail." / "This account is scheduled for deletion. To keep it, use the cancel link sent to your e-mail."

If the i18n setup does not enable `returnObjects`, render arrays with `t('deletion.steps', {returnObjects: true}) as string[]`. Check `lib/i18n.ts`.

- [ ] **Step 2:** `npm test` → still green (JSON only).
- [ ] **Step 3: Commit** — `git add ui/src/locales && git commit -m "feat(ui): account deletion copy"`

---

### Task 3: "Delete account" page and Profile entry

**Files:** Create `ui/src/app/account/delete/page.tsx`, `ui/src/app/account/delete/page.test.tsx`, `ui/src/components/delete-account-card.tsx`; Modify `ui/src/app/account/profile/page.tsx`.

**Interfaces:** Consumes Task 1–2.

- [ ] **Step 1: Failing test** — `ui/src/app/account/delete/page.test.tsx`:

```tsx
import {cleanup, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {QueryClient, QueryClientProvider} from '@tanstack/react-query'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import DeleteAccountPage from './page'
import {fetchDeletionRequest, fetchProfile} from '@/lib/queries'
import {requestDeletionAPI} from '@/lib/mutations'
import {isAxiosError} from '@/lib/axios'

vi.mock('@/lib/queries', () => ({fetchDeletionRequest: vi.fn(), fetchProfile: vi.fn()}))
vi.mock('@/lib/mutations', () => ({requestDeletionAPI: vi.fn()}))
vi.mock('@/lib/axios', () => ({isAxiosError: vi.fn()}))

afterEach(cleanup)

function renderPage() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}})
  return render(<QueryClientProvider client={client}><DeleteAccountPage/></QueryClientProvider>)
}

describe('DeleteAccountPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(fetchDeletionRequest).mockResolvedValue(null)
    vi.mocked(fetchProfile).mockResolvedValue({has_password: true} as never)
  })

  it('submits phrase and password, then asks to check the e-mail', async () => {
    const user = userEvent.setup()
    vi.mocked(requestDeletionAPI).mockResolvedValue({request_id: 'r1', state: 'awaiting_confirmation', confirm_by: '2026-10-08T12:00:00Z'})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    await waitFor(() => expect(requestDeletionAPI).toHaveBeenCalledWith('EXCLUIR MINHA CONTA', 'pw'))
    expect(await screen.findByText(/confirmation link/i)).toBeInTheDocument()
  })

  it('keeps the button disabled until the phrase matches', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'excluir')
    expect(screen.getByRole('button', {name: 'Request deletion'})).toBeDisabled()
  })

  it('shows blockers', async () => {
    const user = userEvent.setup()
    vi.mocked(isAxiosError).mockReturnValue(true)
    vi.mocked(requestDeletionAPI).mockRejectedValue({response: {status: 409, data: {
      type: 'https://accounts.aoctech.app/problems/deletion-blocked',
      blockers: [
        {code: 'wallet.balance_nonzero', action_url: 'https://ledger.aoctech.app/withdraw'},
        {code: 'poker.seated'},
      ],
    }}})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    await user.type(screen.getByLabelText('Account password'), 'pw')
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    expect(await screen.findByText(/CTech Ledger/)).toBeInTheDocument()
    expect(screen.getByRole('link', {name: 'Resolve'})).toHaveAttribute('href', 'https://ledger.aoctech.app/withdraw')
    expect(screen.getByText(/poker\.seated/)).toBeInTheDocument()
  })

  it('without password: no password field, submits phrase only', async () => {
    const user = userEvent.setup()
    vi.mocked(fetchProfile).mockResolvedValue({has_password: false} as never)
    vi.mocked(requestDeletionAPI).mockResolvedValue({request_id: 'r1', state: 'awaiting_confirmation'})
    renderPage()
    await user.type(await screen.findByLabelText(/EXCLUIR MINHA CONTA/), 'EXCLUIR MINHA CONTA')
    expect(screen.queryByLabelText('Account password')).toBeNull()
    await user.click(screen.getByRole('button', {name: 'Request deletion'}))
    await waitFor(() => expect(requestDeletionAPI).toHaveBeenCalledWith('EXCLUIR MINHA CONTA', undefined))
  })
})
```

(The English labels assume the `en.json` strings "Account password", "Request deletion", "Resolve", "To confirm, type {{phrase}}", and a `checkEmail` text containing "confirmation link". Keep them aligned.)

- [ ] **Step 2: Fail** — `npx vitest run src/app/account/delete` → fails (no page).

- [ ] **Step 3: Implement** — `ui/src/app/account/delete/page.tsx`:

```tsx
'use client'

import {useState, type SyntheticEvent} from 'react'
import {useMutation, useQuery, useQueryClient} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Button} from '@/components/ui/button'
import {Card, CardContent, CardHeader, CardTitle} from '@/components/ui/card'
import {Input} from '@/components/ui/input'
import {Label} from '@/components/ui/label'
import {isAxiosError} from '@/lib/axios'
import {DELETION_BLOCKED_PROBLEM, DELETION_PHRASE} from '@/lib/constants'
import {formatDate} from '@/lib/format'
import {requestDeletionAPI} from '@/lib/mutations'
import {fetchDeletionRequest, fetchProfile} from '@/lib/queries'
import type {DeletionBlocker, ProblemDetail} from '@/lib/types'

function problemOf(err: unknown): ProblemDetail | undefined {
  return isAxiosError(err) ? (err as {response?: {data?: ProblemDetail}}).response?.data : undefined
}

function statusOf(err: unknown): number | undefined {
  return isAxiosError(err) ? (err as {response?: {status?: number}}).response?.status : undefined
}

function BlockerList({blockers}: {blockers: DeletionBlocker[]}) {
  const {t} = useTranslation()
  return (
    <Alert variant="destructive">
      <AlertDescription className="space-y-2">
        <p className="font-medium">{t('deletion.blockedTitle')}</p>
        <ul className="list-disc pl-5 space-y-1">
          {blockers.map((b) => (
            <li key={b.code + (b.action_url ?? '')}>
              {t(`deletion.blockers.${b.code}`, {defaultValue: t('deletion.blockers.unknown', {code: b.code})})}
              {b.action_url && (
                <> <a href={b.action_url} className="underline underline-offset-4">{t('deletion.blockerAction')}</a></>
              )}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

export default function DeleteAccountPage() {
  const {t} = useTranslation()
  const queryClient = useQueryClient()
  const [phrase, setPhrase] = useState('')
  const profile = useQuery({queryKey: ['profile'], queryFn: fetchProfile})
  const current = useQuery({queryKey: ['deletion'], queryFn: fetchDeletionRequest})
  const request = useMutation({
    mutationFn: ({password}: {password?: string}) => requestDeletionAPI(DELETION_PHRASE, password),
    onSuccess: () => queryClient.invalidateQueries({queryKey: ['deletion']}),
  })

  function handleSubmit(e: SyntheticEvent<HTMLFormElement>) {
    e.preventDefault()
    const password = new FormData(e.currentTarget).get('password')
    request.mutate({password: typeof password === 'string' && password !== '' ? password : undefined})
  }

  const problem = problemOf(request.error)
  const status = statusOf(request.error)
  const blockers = problem?.type?.endsWith(DELETION_BLOCKED_PROBLEM) ? problem.blockers ?? [] : []
  const errorText = request.isError && blockers.length === 0
    ? status === 401 ? t('deletion.errors.password')
      : status === 409 ? t('deletion.errors.conflict')
        : status === 429 ? t('deletion.errors.tooMany')
          : status === 503 ? t('deletion.errors.unavailable')
            : t('errors.network')
    : ''
  const sent = request.data ?? (current.data?.state === 'awaiting_confirmation' ? current.data : null)

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">{t('deletion.title')}</h1>
        <p className="text-muted-foreground text-sm mt-1">{t('deletion.subtitle')}</p>
      </div>

      {current.data?.legal_hold && (
        <Alert><AlertDescription>{t('deletion.legalHold')}</AlertDescription></Alert>
      )}
      {sent && (
        <Alert>
          <AlertDescription>
            {request.data
              ? t('deletion.checkEmail', {date: formatDate(sent.confirm_by ?? '')})
              : t('deletion.awaitingConfirmation', {date: formatDate(sent.confirm_by ?? '')})}
          </AlertDescription>
        </Alert>
      )}

      <Card>
        <CardHeader><CardTitle>{t('deletion.whatHappens')}</CardTitle></CardHeader>
        <CardContent className="space-y-4 text-sm">
          <ol className="list-decimal pl-5 space-y-1">
            {(t('deletion.steps', {returnObjects: true}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ol>
          <p className="font-medium">{t('deletion.whatIsKept')}</p>
          <ul className="list-disc pl-5 space-y-1">
            {(t('deletion.kept', {returnObjects: true}) as string[]).map((s) => <li key={s}>{s}</li>)}
          </ul>
          <Alert><AlertDescription>{t('deletion.soleOrgsWarning')}</AlertDescription></Alert>
        </CardContent>
      </Card>

      {blockers.length > 0 && <BlockerList blockers={blockers}/>}
      {errorText && <Alert variant="destructive"><AlertDescription>{errorText}</AlertDescription></Alert>}

      {!request.isSuccess && (
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="phrase">{t('deletion.phraseLabel', {phrase: DELETION_PHRASE})}</Label>
            <Input id="phrase" value={phrase} onChange={(e) => setPhrase(e.target.value)} autoComplete="off"/>
          </div>
          {profile.data?.has_password ? (
            <div className="space-y-1.5">
              <Label htmlFor="password">{t('deletion.passwordLabel')}</Label>
              <Input id="password" name="password" type="password" autoComplete="current-password" required/>
            </div>
          ) : profile.data ? (
            <p className="text-sm text-muted-foreground">{t('deletion.noPasswordHint')}</p>
          ) : null}
          <Button type="submit" variant="destructive" disabled={phrase !== DELETION_PHRASE || request.isPending || !profile.data}>
            {request.isPending ? t('common.loading') : t('deletion.submit')}
          </Button>
        </form>
      )}
    </div>
  )
}
```

Check:
- `formatDate(dateStr: string | null)` is the date helper in `lib/format.ts`.
- `Button` has a `destructive` variant (ShadCN default).
- `isAxiosError` is exported from `lib/axios.ts` (CLAUDE.md says so).

`ui/src/components/delete-account-card.tsx`:

```tsx
'use client'

import Link from 'next/link'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {Card, CardContent, CardDescription, CardHeader, CardTitle} from '@/components/ui/card'

/** Profile entry point to account deletion: a quiet, separate danger zone. */
export function DeleteAccountCard() {
  const {t} = useTranslation()
  return (
    <Card className="border-destructive/40">
      <CardHeader>
        <CardTitle>{t('deletion.cardTitle')}</CardTitle>
        <CardDescription>{t('deletion.cardDesc')}</CardDescription>
      </CardHeader>
      <CardContent>
        <Button variant="destructive" render={<Link href="/account/delete"/>}>{t('deletion.cardAction')}</Button>
      </CardContent>
    </Card>
  )
}
```

`app/account/profile/page.tsx` — after the password `</Card>`, before the closing `</div>`:

```tsx
      <Separator />

      <DeleteAccountCard />
```

(import `DeleteAccountCard` from `@/components/delete-account-card`).

- [ ] **Step 4: Pass** — `npx vitest run src/app/account/delete && npx eslint src --ext .ts,.tsx` → clean.
- [ ] **Step 5: Commit** — `git add ui/src && git commit -m "feat(ui): delete-account page and profile entry"`

---

### Task 4: E-mail link pages — confirm and cancel

**Files:** Create `ui/src/app/account-deletion/confirm/page.tsx`, `ui/src/app/account-deletion/cancel/page.tsx`, `ui/src/components/deletion-link-page.tsx`, `ui/src/components/deletion-link-page.test.tsx`.

One shared component renders both pages: title, description, one button. It acts only on click.

- [ ] **Step 1: Failing test** — `ui/src/components/deletion-link-page.test.tsx`:

```tsx
import {cleanup, render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {DeletionLinkPage} from './deletion-link-page'

vi.mock('@/lib/axios', () => ({isAxiosError: () => true}))
vi.mock('@/store/auth', () => ({useAuthStore: {getState: () => ({clearAuth: vi.fn()})}}))

afterEach(cleanup)

describe('DeletionLinkPage', () => {
  it('TestConfirmPage_DoesNothingUntilClicked', async () => {
    const action = vi.fn().mockResolvedValue({request_id: 'r1', state: 'pending_deletion', grace_until: '2026-10-14T12:00:00Z'})
    const user = userEvent.setup()
    render(<DeletionLinkPage kind="confirm" requestID="r1" token="t1" action={action}/>)
    expect(action).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', {name: 'Confirm deletion'}))
    expect(action).toHaveBeenCalledWith('r1', 't1')
    expect(await screen.findByText(/cancel/i)).toBeInTheDocument()
  })

  it('CancelPage distinguishes too-late from invalid', async () => {
    const user = userEvent.setup()
    const tooLate = vi.fn().mockRejectedValue({response: {status: 409}})
    render(<DeletionLinkPage kind="cancel" requestID="r1" token="t1" action={tooLate}/>)
    await user.click(screen.getByRole('button', {name: 'Cancel deletion'}))
    expect(await screen.findByText(/can no longer be cancelled/i)).toBeInTheDocument()
    cleanup()
    const invalid = vi.fn().mockRejectedValue({response: {status: 401}})
    render(<DeletionLinkPage kind="cancel" requestID="r1" token="t1" action={invalid}/>)
    await user.click(screen.getByRole('button', {name: 'Cancel deletion'}))
    expect(await screen.findByText(/link is invalid/i)).toBeInTheDocument()
  })

  it('shows the invalid message when the link lacks parameters', () => {
    render(<DeletionLinkPage kind="confirm" requestID="" token="" action={vi.fn()}/>)
    expect(screen.queryByRole('button')).toBeNull()
  })
})
```

(The English copy must contain "cancel" in `confirm.done`, "can no longer be cancelled" in `cancel.tooLate`, and "link is invalid" in `cancel.invalid`.)

- [ ] **Step 2: Fail** — `npx vitest run src/components/deletion-link-page.test.tsx` → fails.

- [ ] **Step 3: Implement** — `ui/src/components/deletion-link-page.tsx`:

```tsx
'use client'

import Link from 'next/link'
import {useMutation} from '@tanstack/react-query'
import {useTranslation} from 'react-i18next'
import {Alert, AlertDescription} from '@/components/ui/alert'
import {Button} from '@/components/ui/button'
import {Card, CardContent} from '@/components/ui/card'
import {isAxiosError} from '@/lib/axios'
import {formatDate} from '@/lib/format'
import type {DeletionRequest} from '@/lib/types'
import {useAuthStore} from '@/store/auth'

type Props = {
  kind: 'confirm' | 'cancel'
  requestID: string
  token: string
  action: (requestID: string, token: string) => Promise<DeletionRequest>
}

/**
 * The page an account-deletion e-mail link opens. It acts only when the user
 * presses the button (a POST): mail scanners that prefetch links must never
 * confirm or cancel a deletion.
 */
export function DeletionLinkPage({kind, requestID, token, action}: Props) {
  const {t} = useTranslation()
  const run = useMutation({
    mutationFn: () => action(requestID, token),
    onSuccess: () => {
      if (kind === 'confirm') useAuthStore.getState().clearAuth() // every session was just revoked
    },
  })
  const status = isAxiosError(run.error) ? (run.error as {response?: {status?: number}}).response?.status : undefined
  const invalid = !requestID || !token || status === 401 || status === 404
  const message = run.isSuccess
    ? kind === 'confirm'
      ? t('deletion.confirm.done', {date: formatDate(run.data.grace_until ?? '')})
      : t('deletion.cancel.done')
    : invalid
      ? t(`deletion.${kind}.invalid`)
      : status === 409
        ? t('deletion.cancel.tooLate')
        : run.isError
          ? t('errors.network')
          : ''

  return (
    <div className="min-h-screen flex items-center justify-center bg-muted/40 p-4">
      <div className="w-full max-w-md space-y-6">
        <div className="text-center space-y-1">
          <p className="text-sm font-medium text-muted-foreground">{t('app.name')}</p>
          <h1 className="text-2xl font-semibold tracking-tight">{t(`deletion.${kind}.title`)}</h1>
          <p className="text-muted-foreground text-sm">{t(`deletion.${kind}.description`)}</p>
        </div>
        <Card>
          <CardContent className="space-y-4">
            {message && (
              <Alert variant={run.isSuccess ? 'default' : 'destructive'}>
                <AlertDescription>{message}</AlertDescription>
              </Alert>
            )}
            {!run.isSuccess && !invalid && (
              <Button className="w-full" variant={kind === 'confirm' ? 'destructive' : 'default'}
                      disabled={run.isPending} onClick={() => run.mutate()}>
                {run.isPending ? t('common.loading') : t(`deletion.${kind}.button`)}
              </Button>
            )}
            <p className="text-center text-sm text-muted-foreground">
              <Link href="/login" className="underline underline-offset-4 hover:text-foreground">{t('deletion.goToLogin')}</Link>
            </p>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
```

The component uses `useMutation`, so the test must wrap it in a `QueryClientProvider`. Add a `renderWithClient` helper to the test file, the same as `renderPage` in Task 3, and use it in every `render(...)` call above.

`ui/src/app/account-deletion/confirm/page.tsx`:

```tsx
'use client'

import {Suspense} from 'react'
import {useSearchParams} from 'next/navigation'
import {DeletionLinkPage} from '@/components/deletion-link-page'
import {confirmDeletionAPI} from '@/lib/mutations'

function ConfirmFromLink() {
  const params = useSearchParams()
  return <DeletionLinkPage kind="confirm" requestID={params.get('request') ?? ''} token={params.get('token') ?? ''} action={confirmDeletionAPI}/>
}

export default function ConfirmDeletionPage() {
  return <Suspense fallback={null}><ConfirmFromLink/></Suspense>
}
```

`cancel/page.tsx` is the same, with `kind="cancel"` and `cancelDeletionAPI`.

- [ ] **Step 4: Pass** — `npx vitest run src/components/deletion-link-page.test.tsx && npm run build` → pass; the build lists `/account-deletion/confirm` and `/account-deletion/cancel`.
- [ ] **Step 5: Commit** — `git add ui/src && git commit -m "feat(ui): confirm and cancel pages for deletion e-mail links"`

---

### Task 5: Login messages for a pending account

**Files:** Modify `ui/src/app/login/page.tsx`; Test `ui/src/app/login/page.test.tsx`.

- [ ] **Step 1: Failing tests** — append to `ui/src/app/login/page.test.tsx`, following the file's existing setup (its mocks for `@/lib/mutations` login and for `useSearchParams`):
  - "login shows pending-deletion message": make the password-login mutation reject with `{response: {status: 403, data: {type: 'https://accounts.aoctech.app/problems/account-pending-deletion', detail: 'x'}}}` and `isAxiosError → true`. Submit. Expect the English `errors.accountPendingDeletion` text.
  - Google redirect: render with search params `?error=account_pending_deletion` and expect the same text.

  Write both with the same helpers and assertions the file's existing error tests use.

- [ ] **Step 2: Fail** — `npx vitest run src/app/login/page.test.tsx` → the two new tests fail.

- [ ] **Step 3: Implement** — in `login/page.tsx`:
  - add `account_pending_deletion: t('errors.accountPendingDeletion'),` to `oauthErrorMessages`;
  - in the password-login and passkey `catch` blocks, before the generic `setError(...response?.data?.detail ?? ...)`:

```tsx
        if (String(submitError.response?.data?.type ?? '').endsWith(ACCOUNT_PENDING_DELETION_PROBLEM)) {
          setError(t('errors.accountPendingDeletion'))
          return
        }
```

  (use `passkeyError` in the passkey block; import `ACCOUNT_PENDING_DELETION_PROBLEM` from `@/lib/constants`). Check that `return` is valid inside each catch; otherwise wrap the generic branch in `else`.

- [ ] **Step 4: Pass** — `npx vitest run src/app/login && npx eslint src --ext .ts,.tsx` → clean.
- [ ] **Step 5: Commit** — `git add ui/src && git commit -m "feat(ui): explain pending deletion on login"`

---

### Task 6: Privacy Policy 3.3 (requires legal sign-off — R15)

**Files:**
- Create `ui/src/app/privacy/v3/page.tsx` (frozen 3.2)
- Modify `ui/src/app/privacy/page.tsx`, `ui/src/components/legal-page-layout.tsx` (`PRIVACY_VERSION_HISTORY`), `ui/src/lib/legal-documents.ts` (if it lists privacy versions)
- Modify `api/internal/legal/version.go` (`CurrentPrivacyVersion = "3.3"`)

- [ ] **Step 1: Freeze 3.2.** Copy `app/privacy/page.tsx` verbatim to `app/privacy/v3/page.tsx`, keeping `PRIVACY_VERSION = '3.2'` and its date. This mirrors how `v1`/`v2` were frozen; check `app/privacy/v2/page.tsx` for the exact frozen-page shape (title/metadata) and match it.
- [ ] **Step 2: Update the current page** to version `3.3`, `UPDATED_AT` = the publication date.
  - Append to section **8. Retenção dos dados**:

```tsx
        <p>
          Após a exclusão da conta CTech, mantemos apenas o que a lei exige:
          os registros de verificação de identidade (KYC) e as movimentações
          financeiras do CTech Ledger, por 5 (cinco) anos, para cumprimento da
          Lei nº 9.613/1998; os documentos fiscais de organizações que
          continuam ativas com outros membros, que pertencem à organização; e
          um código irreversível derivado do CPF, para prevenção a fraudes.
          Cópias de segurança expiram em até 35 (trinta e cinco) dias.
        </p>
```

  - Append to section **12. Direitos dos titulares**:

```tsx
        <p>
          A exclusão da conta pode ser solicitada em Perfil › Excluir conta.
          O pedido é confirmado por e-mail; a partir da confirmação a conta
          fica bloqueada e é excluída definitivamente após 7 (sete) dias, prazo
          em que pode ser cancelada pelo link enviado ao e-mail do titular.
          Concluída a exclusão, os dados pessoais são eliminados ou
          anonimizados em todos os produtos CTech, salvo as hipóteses de
          retenção do item 8. Organizações em que o titular é o único membro
          são excluídas junto com a conta, inclusive seus documentos fiscais;
          recomendamos exportá-los antes.
        </p>
```

- [ ] **Step 3: Version history.** Prepend `{version: '3.3', updatedAt: '<date>', href: '/privacy'}` to `PRIVACY_VERSION_HISTORY` and change the 3.2 entry's `href` to `/privacy/v3`.
- [ ] **Step 4: API.** In `api/internal/legal/version.go` set `CurrentPrivacyVersion = "3.3"`. Every user then re-accepts through the existing terms gate. Run `cd api && go test ./...`; tests that pin "3.2" must be updated to use the constant.
- [ ] **Step 5:** `cd ui && npm test && npm run build` → clean.
- [ ] **Step 6: Stop and ask the user** to confirm the text was approved by legal before committing or merging this task. Then: `git add ui/src api/internal/legal && git commit -m "feat(legal): privacy policy 3.3 — account deletion and retention"`

---

### Task 7: Documentation

- [ ] **`ui/FRONTEND.md`**: the three new routes (`/account/delete`, `/account-deletion/confirm`, `/account-deletion/cancel`), the click-only rule for e-mail-link pages, and the profile entry point.
- [ ] **`PLAN.md`**: check Phase 4.
- [ ] **Commit** — `git add ui/FRONTEND.md PLAN.md && git commit -m "docs: account deletion UI"`

## Cross-project impact

- **ctech-account api:** `CurrentPrivacyVersion` bump (Task 6) makes every user re-accept the privacy policy.
- **Products (dfe, wallet, billing, poker):** blocker codes they return should have pt-BR/en copy under `deletion.blockers.<code>`. Unknown codes fall back to a generic line. Add copy as each participant plan defines its codes (e.g. `wallet.balance_nonzero` is already included).
- **ctech-ui:** none. This page uses local ShadCN components. If `ctech-ui` gains a shared danger-zone/confirm pattern, migrate then.
