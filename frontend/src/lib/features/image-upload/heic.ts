// Client-side HEIC/HEIF handling for direct-to-storage uploads. iPhones
// capture HEIC by default, which Chrome and Firefox cannot decode, so a picked
// HEIC photo is converted to JPEG before its bytes reach storage. The backend
// stays a byte stager (see the review-photo ADR); no server-side codecs.
// The decoder (heic-to, libheif under LGPL-3.0) is imported dynamically: it
// only loads for the first HEIC pick, so other photos pay no bundle cost.
const HEIC_MIME_TYPES = new Set(['image/heic', 'image/heif']);

// Types Chrome and Firefox can already display; they are passed through
// untouched even when a browser mislabels a HEIC file with one of them.
const DECODABLE_IMAGE_TYPES = new Set([
	'image/jpeg',
	'image/png',
	'image/webp',
	'image/gif',
	'image/bmp',
	'image/avif',
	'image/svg+xml'
]);

// ISO base media file type box brands that indicate a HEIC/HEIF container.
// AVIF shares the box structure but browsers decode it natively, so it stays
// out of this list.
const HEIC_FILE_BRANDS = new Set([
	'heic',
	'heix',
	'hevc',
	'hevx',
	'heim',
	'heis',
	'hevm',
	'hevs',
	'mif1',
	'mif2',
	'msf1'
]);

const fourCC = (bytes: Uint8Array, offset: number): string =>
	String.fromCharCode(bytes[offset], bytes[offset + 1], bytes[offset + 2], bytes[offset + 3]);

/**
 * Sync gate for the conversion path. HEIC/HEIF types convert; JPEG, PNG,
 * WebP and other decodable types never do. Unknown, missing or mislabelled
 * types need a magic-byte sniff (see isHeicFile) before deciding.
 */
export function couldBeHeic(type: string): boolean {
	if (HEIC_MIME_TYPES.has(type)) return true;
	return !DECODABLE_IMAGE_TYPES.has(type);
}

/**
 * Magic-byte sniff for files whose MIME type is missing or unknown. A HEIC
 * file always starts with an ISOBMFF box whose header at byte 4 reads
 * 'ftyp' followed by a brand we recognise as HEIF.
 */
export function sniffsHeic(head: Uint8Array): boolean {
	if (head.length < 12) return false;
	if (fourCC(head, 4) !== 'ftyp') return false;
	return HEIC_FILE_BRANDS.has(fourCC(head, 8).toLowerCase());
}

export async function isHeicFile(file: Blob, mimeType: string): Promise<boolean> {
	if (!couldBeHeic(mimeType)) return false;
	const head = new Uint8Array(await file.slice(0, 12).arrayBuffer());
	return sniffsHeic(head);
}

/** HEIC picks upload as JPEG, so the name gets the matching extension. */
export function jpegFilename(name: string): string {
	const dot = name.lastIndexOf('.');
	const base = dot > 0 ? name.slice(0, dot) : name.replace(/^\.h(eic|eif)$/i, '');
	return `${base || 'photo'}.jpg`;
}

/**
 * Converts one picked file to JPEG at quality 0.85 when it is a decodable
 * HEIC/HEIF photo, and hands back the original file otherwise. A file the
 * decoder cannot read keeps its original bytes so the existing row
 * validation and the storage contract decide its fate; conversion never
 * blocks a pick with a new hard failure.
 */
export async function transcodeHeicFile(file: File): Promise<File> {
	try {
		if (!(await isHeicFile(file, file.type))) return file;
		const { heicTo } = await import('heic-to');
		const jpeg = await heicTo({ blob: file, type: 'image/jpeg', quality: 0.85 });
		return new File([jpeg], jpegFilename(file.name), { type: 'image/jpeg' });
	} catch {
		// Decoding failed or the decoder could not even load: keep the original
		// HEIC file rather than rejecting it outright.
		return file;
	}
}
