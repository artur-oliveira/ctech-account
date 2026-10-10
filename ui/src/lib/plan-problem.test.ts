import {AxiosError, AxiosHeaders} from 'axios'
import {describe, expect, it, vi} from 'vitest'
import {planProblemOf, planURL} from './plan-problem'

vi.mock('@/lib/env', async (orig) => ({...(await orig<object>()), BILLING_URL: 'https://billing.example'}))

function problem(status: number, data: unknown) {
  return new AxiosError('x', String(status), undefined, undefined, {
    status, data, statusText: '', headers: {}, config: {headers: new AxiosHeaders()},
  })
}

describe('planProblemOf', () => {
  it('reads a plan limit with its numbers', () => {
    expect(planProblemOf(problem(402, {code: 'plan_limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'})))
      .toEqual({kind: 'limit', resource: 'spaces', limit: 1, used: 1, plan: 'free'})
  })

  it('reads a transfer refusal that carries no numbers', () => {
    expect(planProblemOf(problem(402, {code: 'plan_limit', resource: 'spaces'})))
      .toEqual({kind: 'limit', resource: 'spaces', limit: undefined, used: undefined, plan: undefined})
  })

  it('reads an unavailable plan', () => {
    expect(planProblemOf(problem(503, {code: 'plan_unavailable'}))).toEqual({kind: 'unavailable'})
  })

  it('ignores every other failure', () => {
    expect(planProblemOf(problem(503, {code: 'service_unavailable'}))).toBeNull()
    expect(planProblemOf(problem(403, {}))).toBeNull()
    expect(planProblemOf(new Error('x'))).toBeNull()
  })

  it('builds the plans link on the billing app', () => {
    expect(planURL()).toBe('https://billing.example/finance/plans')
  })
})
