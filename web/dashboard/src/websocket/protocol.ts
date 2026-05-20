// GoToIM binary protocol codec (TypeScript port)
// Based on api/protocol/protocol.go

// Operation codes from api/protocol/operation.go
export const OP_HANDSHAKE = 0
export const OP_HANDSHAKE_REPLY = 1
export const OP_HEARTBEAT = 2
export const OP_HEARTBEAT_REPLY = 3
export const OP_SEND_MSG = 4
export const OP_SEND_MSG_REPLY = 5
export const OP_DISCONNECT_REPLY = 6
export const OP_AUTH = 7
export const OP_AUTH_REPLY = 8
export const OP_RAW = 9
export const OP_PROTO_READY = 10
export const OP_PROTO_FINISH = 11
export const OP_CHANGE_ROOM = 12
export const OP_CHANGE_ROOM_REPLY = 13
export const OP_SUB = 14
export const OP_SUB_REPLY = 15
export const OP_UNSUB = 16
export const OP_UNSUB_REPLY = 17
export const OP_SEND_MSG_ACK = 18
export const OP_PUSH_MSG_ACK = 19
export const OP_SYNC_REQ = 20
export const OP_SYNC_REPLY = 21
export const OP_KICK_CONNECTION = 22

const HEADER_LEN = 16

export interface Proto {
  packLen: number
  headerLen: number
  ver: number
  op: number
  seq: number
  body: Uint8Array
}

export function buildProto(op: number, body: Uint8Array, seq: number = 0): ArrayBuffer {
  const packLen = HEADER_LEN + body.byteLength
  const buf = new ArrayBuffer(packLen)
  const view = new DataView(buf)

  view.setInt32(0, packLen, false) // packLen
  view.setInt16(4, HEADER_LEN, false) // headerLen
  view.setInt16(6, 1, false) // ver
  view.setInt32(8, op, false) // op
  view.setInt32(12, seq, false) // seq

  const bodyArr = new Uint8Array(buf, HEADER_LEN)
  bodyArr.set(body)

  return buf
}

export function parseProto(buf: ArrayBuffer): Proto {
  const view = new DataView(buf)
  const packLen = view.getInt32(0, false)
  const headerLen = view.getInt16(4, false)
  const ver = view.getInt16(6, false)
  const op = view.getInt32(8, false)
  const seq = view.getInt32(12, false)
  const body = new Uint8Array(buf, headerLen, packLen - headerLen)

  return { packLen, headerLen, ver, op, seq, body }
}

// JSON body helpers for auth
export function buildAuthBody(mid: string, key: string, roomId: string, platform: string, deviceId: string, accepts: number[], lastSeq: number): Uint8Array {
  const enc = new TextEncoder()
  const numericMid = Number(mid)

  if (!Number.isFinite(numericMid)) {
    throw new Error(`Invalid GoIM auth mid: ${mid}`)
  }

  return enc.encode(JSON.stringify({
    mid: numericMid,
    key,
    room_id: roomId,
    platform,
    device_id: deviceId,
    accepts,
    last_seq: lastSeq,
  }))
}

export function buildAckBody(msgId: string, seq: number): Uint8Array {
  const msgIDBytes = new TextEncoder().encode(msgId)
  const buf = new ArrayBuffer(2 + msgIDBytes.byteLength + 8 + 1)
  const view = new DataView(buf)
  view.setUint16(0, msgIDBytes.byteLength, false)
  new Uint8Array(buf, 2, msgIDBytes.byteLength).set(msgIDBytes)
  view.setBigInt64(2 + msgIDBytes.byteLength, BigInt(seq), false)
  view.setUint8(2 + msgIDBytes.byteLength + 8, 0)
  return new Uint8Array(buf)
}

export function parsePushBody(body: Uint8Array): Record<string, unknown> | null {
  const direct = parseJSONBody(body)
  if (direct) return direct

  const msgBody = parseMsgBody(body)
  if (!msgBody) return null
  const parsed = parseJSONBody(msgBody.content)
  if (!parsed) return null
  if (!parsed.msg_id) parsed.msg_id = msgBody.msgId
  if (!parsed.timestamp) parsed.timestamp = msgBody.timestamp
  if (!parsed.seq) parsed.seq = msgBody.seq
  return parsed
}

function parseJSONBody(body: Uint8Array): Record<string, unknown> | null {
  try {
    const dec = new TextDecoder()
    const json = dec.decode(body)
    return JSON.parse(json)
  } catch {
    return null
  }
}

function parseMsgBody(body: Uint8Array): { msgId: string; timestamp: number; seq: number; content: Uint8Array } | null {
  if (body.byteLength < 34) return null
  const view = new DataView(body.buffer, body.byteOffset, body.byteLength)
  const msgIDLen = view.getUint16(0, false)
  const headerLen = 2 + msgIDLen + 32
  if (body.byteLength < headerLen) return null
  const msgId = new TextDecoder().decode(body.slice(2, 2 + msgIDLen))
  let offset = 2 + msgIDLen
  offset += 8 // from_uid
  offset += 8 // to_uid
  const timestamp = Number(view.getBigInt64(offset, false))
  offset += 8
  const seq = Number(view.getBigInt64(offset, false))
  offset += 8
  return { msgId, timestamp, seq, content: body.slice(offset) }
}
