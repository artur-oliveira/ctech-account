/**
 * Support ticket subject catalog: one entry per `subject_category` value the
 * backend accepts (`api/internal/domain/support/model.go`'s `ValidCategories`).
 * Every category except `other` carries a fixed subcategory catalog — the
 * user picks both, and the two labels are merged into one human-readable
 * string sent as `subject_other` (there is no separate subcategory field on
 * the backend). `other` has no subcategory catalog: the user free-types the
 * subject instead, exactly as `subject_other` already worked before.
 */
export type SupportSubcategory = { value: string; label: string }
export type SupportCategory = { value: string; label: string; subcategories: SupportSubcategory[] }

export const SUPPORT_CATEGORIES: SupportCategory[] = [
  {
    value: 'account',
    label: 'Conta e login',
    subcategories: [
      { value: 'login_issue', label: 'Não consigo fazer login' },
      { value: 'password_reset', label: 'Esqueci minha senha' },
      { value: 'email_verification', label: 'Verificação de e-mail' },
      { value: 'mfa_passkeys', label: 'Autenticação em duas etapas ou passkeys' },
      { value: 'profile_data', label: 'Alterar dados cadastrais' },
    ],
  },
  {
    value: 'kyc',
    label: 'KYC e verificação',
    subcategories: [
      { value: 'documents_rejected', label: 'Documentos reprovados' },
      { value: 'review_delay', label: 'Verificação demorando' },
      { value: 'change_data', label: 'Alterar CPF ou dados enviados' },
    ],
  },
  {
    value: 'wallet',
    label: 'Wallet',
    subcategories: [
      { value: 'deposit', label: 'Problema com depósito' },
      { value: 'withdrawal', label: 'Problema com saque' },
      { value: 'balance', label: 'Saldo incorreto' },
    ],
  },
  {
    value: 'dfe',
    label: 'DF-e',
    subcategories: [
      { value: 'issue_note', label: 'Erro ao emitir nota' },
      { value: 'cancel_note', label: 'Cancelamento de nota' },
      { value: 'other_dfe', label: 'Outro assunto de DF-e' },
    ],
  },
  {
    value: 'billing',
    label: 'Billing',
    subcategories: [
      { value: 'wrong_charge', label: 'Cobrança indevida' },
      { value: 'invoice', label: 'Nota fiscal ou recibo' },
      { value: 'cancel_plan', label: 'Cancelamento de plano' },
    ],
  },
  {
    value: 'poker',
    label: 'Poker',
    subcategories: [
      { value: 'table_issue', label: 'Problema em mesa ou torneio' },
      { value: 'chips_balance', label: 'Fichas ou saldo' },
      { value: 'player_conduct', label: 'Conduta de outro jogador' },
    ],
  },
  {
    value: 'other',
    label: 'Outro',
    subcategories: [],
  },
]

export function findSupportCategory(value: string): SupportCategory | undefined {
  return SUPPORT_CATEGORIES.find((c) => c.value === value)
}

/**
 * Builds the single merged subject string sent to the backend as
 * `subject_other`: "<category label> — <subcategory label>" for a
 * cataloged category, or the free-typed text as-is for `other`.
 */
export function buildSupportSubject(categoryValue: string, subcategoryValue: string, freeText: string): string {
  const category = findSupportCategory(categoryValue)
  if (!category || category.value === 'other') {
    return freeText.trim()
  }
  const subcategory = category.subcategories.find((s) => s.value === subcategoryValue)
  if (!subcategory) {
    return category.label
  }
  return `${category.label} — ${subcategory.label}`
}
