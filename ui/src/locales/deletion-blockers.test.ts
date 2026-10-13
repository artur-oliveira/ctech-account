import {describe, expect, it} from 'vitest'
import en from './en.json'
import ptBR from './pt-BR.json'

// Codes emitted by participants' eligibility endpoints (ctech-wallet
// domain/wallet/erasure.go, ctech-poker userpurge/eligibility.go). A code
// without a string falls back to the generic "contact support" message.
const PARTICIPANT_BLOCKER_CODES = [
  'wallet.balance_nonzero', 'wallet.hold_open', 'wallet.deposit_pending',
  'wallet.withdrawal_pending', 'wallet.purchase_pending', 'wallet.deposit_quarantined',
  'poker.seated_at_table', 'poker.chips_held', 'poker.pending_cashout', 'poker.pending_fee_debit',
]

describe('deletion blocker translations', () => {
  for (const [lang, locale] of [['en', en], ['pt-BR', ptBR]] as const) {
    it(`${lang} translates every participant blocker code`, () => {
      const blockers = locale.deletion.blockers as Record<string, string>
      for (const code of PARTICIPANT_BLOCKER_CODES) expect(blockers[code], code).toBeTruthy()
    })
  }
})
