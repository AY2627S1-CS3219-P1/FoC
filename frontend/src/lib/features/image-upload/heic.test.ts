import { describe, expect, it, vi } from 'vitest';
import { couldBeHeic, isHeicFile, jpegFilename, sniffsHeic, transcodeHeicFile } from './heic';

vi.mock('heic-to', () => ({
	heicTo: vi.fn(async () => new Blob(['jpeg-bytes'], { type: 'image/jpeg' }))
}));

// Builds the first 12 bytes of an ISOBMFF file: a length word, 'ftyp' and the
// major brand, as found at the start of every HEIC/HEIF (and AVIF) file.
const ftypHeader = (brand: string, extra = ''): Uint8Array<ArrayBuffer> => {
	const bytes = new Uint8Array(12 + extra.length);
	new DataView(bytes.buffer).setUint32(0, 24 + extra.length);
	for (const [offset, text] of [[4, 'ftyp'], [8, brand]] as const) {
		for (let index = 0; index < 4; index += 1) bytes[offset + index] = text.charCodeAt(index);
	}
	if (extra) bytes.set(new TextEncoder().encode(extra), 12);
	return bytes;
};

describe('heic type detection', () => {
	it('converts HEIC and HEIF mime types', () => {
		expect(couldBeHeic('image/heic')).toBe(true);
		expect(couldBeHeic('image/heif')).toBe(true);
	});

	it('never converts types browsers can already decode', () => {
		for (const type of ['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'image/avif']) {
			expect(couldBeHeic(type)).toBe(false);
		}
	});

	it('marks unknown, empty and mislabelled types for a sniff', () => {
		for (const type of ['', 'application/octet-stream', 'image/jxl', 'image/jpg']) {
			expect(couldBeHeic(type)).toBe(true);
		}
	});
});

describe('heic magic-byte sniff', () => {
	it('recognises HEIC and HEIF brands', () => {
		for (const brand of ['heic', 'heix', 'hevc', 'heim', 'heis', 'mif1', 'msf1']) {
			expect(sniffsHeic(ftypHeader(brand))).toBe(true);
		}
	});

	it('rejects avif and non-HEIF containers', () => {
		expect(sniffsHeic(ftypHeader('avif'))).toBe(false);
		expect(sniffsHeic(ftypHeader('isom'))).toBe(false);
		expect(sniffsHeic(new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))).toBe(false);
	});

	it('rejects truncated heads without a valid ftyp box', () => {
		expect(sniffsHeic(ftypHeader('heic', '').slice(0, 4))).toBe(false);
		expect(sniffsHeic(new Uint8Array())).toBe(false);
		expect(sniffsHeic(ftypHeader('heic').slice(1))).toBe(false);
	});

	it('brands are matched case-insensitively', () => {
		expect(sniffsHeic(ftypHeader('HEIC'))).toBe(true);
	});
});

describe('jpeg filename', () => {
	it('replaces a HEIC extension with jpg', () => {
		expect(jpegFilename('IMG_0001.heic')).toBe('IMG_0001.jpg');
		expect(jpegFilename('IMG_0001.HEIF')).toBe('IMG_0001.jpg');
	});

	it('appends jpg to names without an extension it can replace', () => {
		expect(jpegFilename('phone photo')).toBe('phone photo.jpg');
		expect(jpegFilename('.heic')).toBe('photo.jpg');
	});
});

describe('heic passthrough and conversion', () => {
	it('leaves files that are not HEIC untouched', async () => {
		const original = new File([new Uint8Array([0xff, 0xd8, 0xff])], 'photo.jpg', { type: 'image/jpeg' });
		await expect(transcodeHeicFile(original)).resolves.toBe(original);
	});

	it('sniffs HEIC bytes when the MIME type is missing', async () => {
		const named = new File([ftypHeader('heic', 'minor')], 'photo.blob', { type: '' });
		await expect(isHeicFile(named, named.type)).resolves.toBe(true);
		const pretender = new File([ftypHeader('isom')], 'video.blob', { type: '' });
		await expect(isHeicFile(pretender, pretender.type)).resolves.toBe(false);
	});

	it('converts a HEIC file to a JPEG with matching name and type', async () => {
		const heic = new File([ftypHeader('heic')], 'IMG_0001.heic', { type: 'image/heic' });
		const converted = await transcodeHeicFile(heic);
		expect(converted).not.toBe(heic);
		expect(converted.type).toBe('image/jpeg');
		expect(converted.name).toBe('IMG_0001.jpg');
		expect(await converted.text()).toBe('jpeg-bytes');
	});

	it('keeps the original file when the decoder cannot read it', async () => {
		const broken = vi.fn(async () => { throw new Error('decode failed'); });
		const { heicTo } = await import('heic-to');
		vi.mocked(heicTo).mockImplementationOnce(broken);
		// Valid HEIC header but unreadable content: a truncated or corrupt shot.
		const heic = new File([ftypHeader('heic')], 'IMG_0001.heic', { type: 'image/heic' });
		await expect(transcodeHeicFile(heic)).resolves.toBe(heic);
	});
});
