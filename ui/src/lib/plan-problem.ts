import {isAxiosError} from '@/lib/axios'
import {BILLING_URL} from '@/lib/env'
import {BILLING_PLAN_PATH} from '@/lib/constants'

/** A write refused by the Finanças plan, or a plan that could not be read. */
export type PlanProblem =
  | {kind: 'limit'; resource: 'spaces' | 'people'; limit?: number; used?: number; plan?: string}
  | {kind: 'unavailable'}

interface PlanProblemBody {
  code?: string
  resource?: string
  limit?: number
  used?: number
  plan?: string
}

/** By the problem's `code`, never its prose: the detail is English and for logs. */
export function planProblemOf(error: unknown): PlanProblem | null {
  if (!isAxiosError(error)) return null
  const status = error.response?.status
  const data = error.response?.data as PlanProblemBody | undefined
  if (status === 402 && data?.code === 'plan_limit') {
    return {
      kind: 'limit',
      resource: data.resource === 'people' ? 'people' : 'spaces',
      limit: data.limit,
      used: data.used,
      plan: data.plan,
    }
  }
  if (status === 503 && data?.code === 'plan_unavailable') return {kind: 'unavailable'}
  return null
}

/** The Finanças plan page, or null when the build does not know the billing app. */
export function planURL(): string | null {
  return BILLING_URL ? `${BILLING_URL}${BILLING_PLAN_PATH}` : null
}
