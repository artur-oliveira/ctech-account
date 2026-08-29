'use client'

import {useCallback} from 'react'
import {useQueryClient} from '@tanstack/react-query'
import {useWebSocket, type WSStatus} from '@aoctech/ws-client'
import {useAuthStore} from '@/store/auth'
import {USE_MOCK} from '@/lib/mock'
import {decodeSupportServerMessage, encodeSupportClientMessage, supportWSOrigin} from '@/lib/ws/support'

export function useSupportRealtime(ticketId: string, anonymousToken = '', admin = false): WSStatus {
  const queryClient = useQueryClient()
  const accessToken = useAuthStore((state) => state.accessToken)
  const credential = anonymousToken || accessToken || ''
  const onMessage = useCallback(() => {
    void queryClient.invalidateQueries({queryKey: admin ? ['admin-support-ticket', ticketId] : ['support-ticket', ticketId]})
    if (admin) {
      void queryClient.invalidateQueries({queryKey: ['admin-support']})
      void queryClient.invalidateQueries({queryKey: ['admin-support-metrics']})
    } else {
      void queryClient.invalidateQueries({queryKey: ['my-support-tickets']})
    }
  }, [admin, queryClient, ticketId])

  const {status} = useWebSocket({
    url: ticketId ? `${supportWSOrigin()}/v1.0/support/tickets/${encodeURIComponent(ticketId)}/ws` : '',
    binaryType: 'arraybuffer',
    encode: encodeSupportClientMessage,
    decode: decodeSupportServerMessage,
    onMessage,
    enabled: !USE_MOCK && Boolean(ticketId && credential),
    authToken: credential || undefined,
  })
  return status
}
