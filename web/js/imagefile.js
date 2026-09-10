// What the panel checks about a file before it uploads it, and how it encodes
// one.
//
// A separate module from session.js because none of this is about the session
// panel: it is about a file, it is pure, and it is the half of the image feature
// that can be tested without a DOM at all.
//
// These limits duplicate internal/server/image.go, which is uncomfortable and
// deliberate. The server's checks are the defence; these exist only so a person
// learns about a refusal before sending megabytes — an interface that uploads
// 9 MiB to be told the ceiling is 8 has made the same mistake as a message
// naming the wrong limit. Duplication that drifts silently is the shape this
// project keeps finding defects in, so web/tests/image-limits.test.js reads the
// Go source and fails when the two disagree.

// Mirrors maxImageBytes in internal/server/image.go.
export const MAX_IMAGE_BYTES = 8 * 1024 * 1024;

// Mirrors imageTypes in internal/server/image.go. The extension is not used for
// anything the panel decides — the server names the file — and is here so a
// refusal can say what is accepted in words a person recognises.
export const IMAGE_TYPES = ["image/png", "image/jpeg", "image/gif", "image/webp"];

// Signatures, as byte prefixes. Each is checked against the file's actual
// content, never against its name or the type the browser guessed: both are
// claims, and the server decides by the bytes.
const SIGNATURES = [
  { type: "image/png", magic: [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a] },
  { type: "image/jpeg", magic: [0xff, 0xd8, 0xff] },
  { type: "image/gif", magic: [0x47, 0x49, 0x46, 0x38] },
];

// WebP is the one that cannot be read from its front alone: "RIFF", four bytes
// of length that may be anything, then "WEBP". Checking only the prefix would
// accept every RIFF container there is — a .wav among them — so both ends are
// compared and the length in between is skipped on purpose.
const RIFF = [0x52, 0x49, 0x46, 0x46];
const WEBP = [0x57, 0x45, 0x42, 0x50];

function startsWith(bytes, magic, offset = 0) {
  if (bytes.length < offset + magic.length) return false;
  return magic.every((byte, index) => bytes[offset + index] === byte);
}

/**
 * detectImageType returns the media type a file's first bytes identify it as, or
 * "" when they identify nothing this panel accepts.
 *
 * A prefix too short to hold the signature is not a match: half of a PNG header
 * is not a PNG, and treating it as one would send the server something it is
 * about to refuse.
 */
export function detectImageType(bytes) {
  for (const { type, magic } of SIGNATURES) {
    if (startsWith(bytes, magic)) return type;
  }
  if (startsWith(bytes, RIFF) && startsWith(bytes, WEBP, 8)) return "image/webp";
  return "";
}

/**
 * toBase64 encodes bytes the way the upload route expects them.
 *
 * Chunked rather than one spread into String.fromCharCode: the argument list is
 * bounded, and an 8 MiB image passed as one call overflows the stack — on the
 * largest file this accepts, which is precisely when it must not.
 */
export function toBase64(bytes) {
  const chunkSize = 0x8000;
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
  }
  return btoa(binary);
}
