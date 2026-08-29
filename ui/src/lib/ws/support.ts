import {ClientMessage, ServerMessage} from '@/lib/api/proto/support'
import {API_URL, WS_URL} from '@/lib/env'

export function supportWSOrigin(): string {
  const origin = WS_URL || API_URL || (typeof window !== 'undefined' ? window.location.origin : '')
  return origin.replace(/^http/, 'ws')
}

export function encodeSupportClientMessage(value: object): ArrayBuffer {
  const message = ClientMessage.fromPartial(value)
  if ((value as {token?: string}).token && !message.type) message.type = 'auth'
  const bytes = ClientMessage.encode(message).finish()
  return bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer
}

export function decodeSupportServerMessage(value: object): ServerMessage {
  if (typeof value === 'string') throw new Error('Support WebSocket requires binary protobuf frames.')
  return ServerMessage.decode(new Uint8Array(value as ArrayBuffer))
}
