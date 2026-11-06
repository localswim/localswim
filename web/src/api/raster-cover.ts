export interface RasterHeader {
  mime: string
  width: number
  height: number
}

const maxBytes = 10 * 1024 * 1024
const maxAxis = 4096
const maxPixels = 4194304

// Keep these structural checks in parity with internal/coverimage.
// The caller supplies the complete length- and digest-verified original.
export function inspectRaster(bytes: Uint8Array): RasterHeader | null {
  if (bytes.length === 0 || bytes.length > maxBytes) return null

  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  if (matches(bytes, 0, "\x89PNG\r\n\x1a\n")) return png(bytes, view)
  if (matches(bytes, 0, "\xff\xd8")) return jpeg(bytes, view)
  if (matches(bytes, 0, "GIF87a") || matches(bytes, 0, "GIF89a")) return gif(bytes, view)
  if (matches(bytes, 0, "RIFF")) return webp(bytes, view)
  return null
}

function matches(bytes: Uint8Array, start: number, signature: string): boolean {
  for (let i = 0; i < signature.length; i++) {
    if (bytes[start + i] !== signature.charCodeAt(i)) return false
  }
  return true
}

function admitted(width: number, height: number): boolean {
  return width > 0 && height > 0 && width <= maxAxis && height <= maxAxis &&
    width * height <= maxPixels
}

function fits(x: number, y: number, width: number, height: number, canvasWidth: number, canvasHeight: number): boolean {
  return width > 0 && height > 0 && x < canvasWidth && y < canvasHeight &&
    width <= canvasWidth - x && height <= canvasHeight - y
}

function png(bytes: Uint8Array, view: DataView): RasterHeader | null {
  if (bytes.length < 33 || view.getUint32(8) !== 13 || !matches(bytes, 12, "IHDR")) return null

  // IEEE CRC-32 over the chunk type and its 13-byte payload.
  let crc = 0xffffffff
  for (let i = 12; i < 29; i++) {
    crc ^= bytes[i]
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0)
    }
  }
  if ((~crc >>> 0) !== view.getUint32(29) || bytes[26] !== 0 || bytes[27] !== 0 || bytes[28] > 1) return null

  const depth = bytes[24]
  switch (bytes[25]) {
    case 0:
      if (![1, 2, 4, 8, 16].includes(depth)) return null
      break
    case 3:
      if (![1, 2, 4, 8].includes(depth)) return null
      break
    case 2:
    case 4:
    case 6:
      if (depth !== 8 && depth !== 16) return null
      break
    default:
      return null
  }

  const width = view.getUint32(16)
  const height = view.getUint32(20)
  return admitted(width, height) ? { mime: "image/png", width, height } : null
}

function jpeg(bytes: Uint8Array, view: DataView): RasterHeader | null {
  let offset = 2
  while (bytes[offset] === 0xff) {
    // Repeated 0xff bytes are legal fill before a marker.
    while (bytes[offset] === 0xff) offset++
    if (bytes.length - offset < 3) return null

    const marker = bytes[offset]
    const size = view.getUint16(offset + 1)
    if (size < 2 || size > bytes.length - offset - 1) return null
    const end = offset + 1 + size
    switch (marker) {
      case 0xc0:
      case 0xc1:
      case 0xc2:
        return jpegFrame(bytes, view, marker, offset + 3, end)
      case 0xc4:
      case 0xdb:
      case 0xdd:
      case 0xfe:
        break
      default:
        if (marker < 0xe0 || marker > 0xef) return null
    }
    offset = end
  }
  return null
}

function jpegFrame(bytes: Uint8Array, view: DataView, marker: number, start: number, end: number): RasterHeader | null {
  if (end - start < 6) return null
  const count = bytes[start + 5]
  const precision = bytes[start]
  if (count === 0 || end - start !== 6 + 3 * count) return null
  if (precision !== 8 && (marker === 0xc0 || precision !== 12)) return null

  const seen = new Set<number>()
  for (let offset = start + 6; offset < end; offset += 3) {
    const id = bytes[offset]
    const horizontal = bytes[offset + 1] >> 4
    const vertical = bytes[offset + 1] & 15
    if (seen.has(id) || horizontal < 1 || horizontal > 4 || vertical < 1 || vertical > 4 || bytes[offset + 2] > 3) return null
    seen.add(id)
  }

  const width = view.getUint16(start + 3)
  const height = view.getUint16(start + 1)
  return admitted(width, height) ? { mime: "image/jpeg", width, height } : null
}

function gif(bytes: Uint8Array, view: DataView): RasterHeader | null {
  if (bytes.length < 13) return null
  const width = view.getUint16(6, true)
  const height = view.getUint16(8, true)
  if (!admitted(width, height)) return null

  let offset = 13 + gifColorTableSize(bytes[10])
  let seenFrame = false
  while (offset >= 0 && offset < bytes.length) {
    switch (bytes[offset++]) {
      case 0x21:
        offset = gifExtensionEnd(bytes, offset)
        break
      case 0x2c:
        offset = gifFrameEnd(bytes, view, offset, width, height)
        seenFrame = true
        break
      case 0x3b:
        return offset === bytes.length && seenFrame ? { mime: "image/gif", width, height } : null
      default:
        return null
    }
  }
  return null
}

function gifColorTableSize(flags: number): number {
  return (flags & 0x80) === 0 ? 0 : 3 * (2 << (flags & 7))
}

// Return the next block offset or -1 for malformed framing.
function gifFrameEnd(bytes: Uint8Array, view: DataView, start: number, width: number, height: number): number {
  if (bytes.length - start < 9 || (bytes[start + 8] & 0x18) !== 0) return -1
  const x = view.getUint16(start, true)
  const y = view.getUint16(start + 2, true)
  const frameWidth = view.getUint16(start + 4, true)
  const frameHeight = view.getUint16(start + 6, true)
  if (!fits(x, y, frameWidth, frameHeight, width, height)) return -1

  const offset = start + 9 + gifColorTableSize(bytes[start + 8])
  if (bytes.length - offset < 2 || bytes[offset] < 2 || bytes[offset] > 8 || bytes[offset + 1] === 0) return -1
  return gifSubBlocksEnd(bytes, offset + 1)
}

function gifExtensionEnd(bytes: Uint8Array, start: number): number {
  if (bytes.length - start < 2) return -1
  switch (bytes[start]) {
    case 0xf9:
      if (bytes.length - start < 7 || bytes[start + 1] !== 4 || bytes[start + 6] !== 0) return -1
      return start + 7
    case 0x01:
      if (bytes[start + 1] !== 12) return -1
      break
    case 0xff:
      if (bytes[start + 1] !== 11) return -1
      break
  }
  return gifSubBlocksEnd(bytes, start + 1)
}

function gifSubBlocksEnd(bytes: Uint8Array, offset: number): number {
  while (offset < bytes.length) {
    const size = bytes[offset++]
    if (size === 0) return offset
    offset += size
  }
  return -1
}

interface WebpChunk {
  kind: string
  start: number
  end: number
  next: number
}

function webp(bytes: Uint8Array, view: DataView): RasterHeader | null {
  if (bytes.length < 12 || view.getUint32(4, true) !== bytes.length - 8 || !matches(bytes, 8, "WEBP")) return null

  const chunk = webpChunk(bytes, view, 12, bytes.length)
  if (chunk === null) return null
  if (chunk.kind === "VP8X") return webpExtended(bytes, view, chunk)
  if ((chunk.kind !== "VP8 " && chunk.kind !== "VP8L") || chunk.next !== bytes.length) return null

  const size = webpRaster(bytes, view, chunk)
  if (size === null || !admitted(size.width, size.height)) return null
  return { mime: "image/webp", ...size }
}

function webpChunk(bytes: Uint8Array, view: DataView, offset: number, limit: number): WebpChunk | null {
  if (limit - offset < 8) return null
  const start = offset + 8
  const size = view.getUint32(offset + 4, true)
  if (size > limit - start) return null

  const end = start + size
  const next = end + (size & 1)
  if (next > limit || (end !== next && bytes[end] !== 0)) return null

  const kind = String.fromCharCode(bytes[offset], bytes[offset + 1], bytes[offset + 2], bytes[offset + 3])
  return { kind, start, end, next }
}

function webpExtended(bytes: Uint8Array, view: DataView, header: WebpChunk): RasterHeader | null {
  const { start, end, next } = header
  if (end - start !== 10 || (bytes[start] & 0xc1) !== 0 || littleUint24(bytes, start + 1) !== 0) return null
  const width = littleUint24(bytes, start + 4) + 1
  const height = littleUint24(bytes, start + 7) + 1
  if (!admitted(width, height)) return null

  const animated = (bytes[start] & 2) !== 0
  const valid = animated
    ? webpAnimation(bytes, view, next, width, height)
    : webpStill(bytes, view, next, bytes.length, width, height, false)
  return valid ? { mime: "image/webp", width, height } : null
}

function webpAnimation(bytes: Uint8Array, view: DataView, offset: number, width: number, height: number): boolean {
  let seenAnimation = false
  let seenFrame = false
  while (offset < bytes.length) {
    const chunk = webpChunk(bytes, view, offset, bytes.length)
    if (chunk === null) return false
    switch (chunk.kind) {
      case "ANIM":
        if (seenAnimation || chunk.end - chunk.start !== 6) return false
        seenAnimation = true
        break
      case "ANMF":
        if (!seenAnimation || !webpFrame(bytes, view, chunk, width, height)) return false
        seenFrame = true
        break
      case "VP8 ":
      case "VP8L":
      case "VP8X":
      case "ALPH":
        return false
    }
    offset = chunk.next
  }
  return seenFrame
}

function webpFrame(bytes: Uint8Array, view: DataView, chunk: WebpChunk, canvasWidth: number, canvasHeight: number): boolean {
  const { start, end } = chunk
  if (end - start < 16 || (bytes[start + 15] & 0xfc) !== 0) return false
  const x = 2 * littleUint24(bytes, start)
  const y = 2 * littleUint24(bytes, start + 3)
  const width = littleUint24(bytes, start + 6) + 1
  const height = littleUint24(bytes, start + 9) + 1
  return fits(x, y, width, height, canvasWidth, canvasHeight) &&
    webpStill(bytes, view, start + 16, end, width, height, true)
}

// Validate one raster stream contained by an extended canvas or frame.
function webpStill(bytes: Uint8Array, view: DataView, offset: number, limit: number, width: number, height: number, frame: boolean): boolean {
  let seenRaster = false
  let seenAlpha = false
  while (offset < limit) {
    const chunk = webpChunk(bytes, view, offset, limit)
    if (chunk === null) return false
    switch (chunk.kind) {
      case "VP8 ":
      case "VP8L": {
        const size = webpRaster(bytes, view, chunk)
        if (seenRaster || (seenAlpha && chunk.kind === "VP8L") || size?.width !== width || size.height !== height) return false
        seenRaster = true
        break
      }
      case "ALPH": {
        const flags = bytes[chunk.start]
        if (seenAlpha || seenRaster || chunk.start === chunk.end ||
          (flags & 0xc0) !== 0 || (flags & 3) > 1 || ((flags >> 4) & 3) > 1) return false
        seenAlpha = true
        break
      }
      case "VP8X":
      case "ANMF":
        return false
      case "ANIM":
        // A still image ignores a top-level animation control chunk.
        if (frame) return false
        break
    }
    offset = chunk.next
  }
  return seenRaster
}

function webpRaster(bytes: Uint8Array, view: DataView, chunk: WebpChunk): { width: number; height: number } | null {
  const { start, end } = chunk
  const length = end - start
  if (chunk.kind === "VP8L") {
    if (length < 5 || bytes[start] !== 0x2f) return null
    const bits = view.getUint32(start + 1, true)
    if (bits >>> 29 !== 0) return null
    return { width: (bits & 0x3fff) + 1, height: ((bits >>> 14) & 0x3fff) + 1 }
  }

  const flags = bytes[start]
  if (length < 10 || (flags & 1) !== 0 || (flags & 0x10) === 0 || ((flags >> 1) & 7) > 3 ||
    !matches(bytes, start + 3, "\x9d\x01\x2a") || (littleUint24(bytes, start) >>> 5) > length - 10) return null
  return { width: view.getUint16(start + 6, true) & 0x3fff, height: view.getUint16(start + 8, true) & 0x3fff }
}

function littleUint24(bytes: Uint8Array, start: number): number {
  return bytes[start] | (bytes[start + 1] << 8) | (bytes[start + 2] << 16)
}
