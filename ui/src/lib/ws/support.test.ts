import {describe, expect, it} from 'vitest'
import {ClientMessage, ServerMessage} from '@/lib/api/proto/support'
import {decodeSupportServerMessage, encodeSupportClientMessage} from '@/lib/ws/support'

describe('support protobuf websocket', () => {
  it('encodes the first credential as an auth frame', () => {
    const bytes = new Uint8Array(encodeSupportClientMessage({token: 'ticket-token'}))
    expect(ClientMessage.decode(bytes)).toMatchObject({type: 'auth', token: 'ticket-token'})
  })

  it('decodes binary ticket events', () => {
    const bytes = ServerMessage.encode(ServerMessage.fromPartial({type: 'message', event: {ticketId: 't1', body: 'Olá'}})).finish()
    const buffer = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer
    expect(decodeSupportServerMessage(buffer)).toMatchObject({type: 'message', event: {ticketId: 't1', body: 'Olá'}})
  })
})
