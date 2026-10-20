import {AxiosError, type AxiosAdapter, type InternalAxiosRequestConfig} from 'axios'
import {describe, expect, it, vi} from 'vitest'
import {api} from '@/lib/axios'

function adapterFailing(times: number, status?: number) {
  let calls = 0
  const adapter: AxiosAdapter = async (config: InternalAxiosRequestConfig) => {
    calls++
    if (calls <= times) {
      throw new AxiosError('boom', 'ERR', config, null, status ? ({status, data: {}, headers: {}, config, statusText: ''} as never) : undefined)
    }
    return {data: {ok: true}, status: 200, statusText: 'OK', headers: {}, config}
  }
  return {adapter, calls: () => calls}
}

describe('api GET retry', () => {
  it('retries a GET after a network error', async () => {
    vi.useFakeTimers()
    const a = adapterFailing(1)
    const p = api.get('/v1.0/x', {adapter: a.adapter})
    await vi.runAllTimersAsync()
    expect((await p).data).toEqual({ok: true})
    expect(a.calls()).toBe(2)
    vi.useRealTimers()
  })

  it('never retries a mutation', async () => {
    const a = adapterFailing(1)
    await expect(api.post('/v1.0/x', {}, {adapter: a.adapter})).rejects.toBeTruthy()
    expect(a.calls()).toBe(1)
  })

  it('does not retry a 4xx GET', async () => {
    const a = adapterFailing(1, 404)
    await expect(api.get('/v1.0/x', {adapter: a.adapter})).rejects.toBeTruthy()
    expect(a.calls()).toBe(1)
  })
})
