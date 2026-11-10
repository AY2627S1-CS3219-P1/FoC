import { describe, expect, it, vi } from 'vitest';
import { couldBeHeic, isHeicFile, jpegFilename, OversizedConversionError, sniffsHeic, transcodeHeicFile } from './heic';

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

describe('heic conversion upload budget', () => {
	const oversizedJpeg = (): Blob => new Blob([new Uint8Array(10_000_001)], { type: 'image/jpeg' });
	const fittingJpeg = (): Blob => new Blob([new Uint8Array(1000)], { type: 'image/jpeg' });

	// Stand-ins for the browser resample path so the budget passes run in
	// tests: each decode sees one 0.7-shrunk tier of a 6000x5000 shot,
	// mirroring a real resample chain feeding its output to the next pass.
	const ORIGIN = { width: 6000, height: 5000 };
	let encode: (quality: number) => Blob;
	let decodes: Array<{ width: number; height: number }>;
	let draws: Array<{ width: number; height: number }>;
	let encodedQualities: number[];

	class StubCanvas {
		readonly width: number;
		readonly height: number;
		constructor(width: number, height: number) {
			this.width = width;
			this.height = height;
		}
		getContext(kind: string) {
			expect(kind).toBe('2d');
			const canvas = this;
			return {
				drawImage(_source: unknown, _x: number, _y: number, width: number, height: number) {
					draws.push({ width, height });
				}
			};
		}
		async convertToBlob(options: { type: string; quality: number }): Promise<Blob> {
			encodedQualities.push(options.quality);
			return encode(options.quality);
		}
	}

	class StubBitmap {
		readonly width: number;
		readonly height: number;
		constructor(width: number, height: number) {
			this.width = width;
			this.height = height;
		}
		close(): void {}
	}

	async function transcode(firstPass: Blob, encodes: (quality: number) => Blob = () => oversizedJpeg()): Promise<File> {
		const { heicTo } = await import('heic-to');
		vi.mocked(heicTo).mockImplementationOnce(async () => firstPass);
		decodes = [];
		draws = [];
		encodedQualities = [];
		encode = encodes;
		vi.stubGlobal('createImageBitmap', async (_blob: Blob) => {
			const tier = decodes.length;
			const bitmap = new StubBitmap(
				Math.round(ORIGIN.width * Math.pow(0.7, tier)),
				Math.round(ORIGIN.height * Math.pow(0.7, tier))
			);
			decodes.push({ width: bitmap.width, height: bitmap.height });
			return bitmap as unknown as ImageBitmap;
		});
		vi.stubGlobal('OffscreenCanvas', StubCanvas as unknown as typeof OffscreenCanvas);
		try {
			const heic = new File([ftypHeader('heic')], 'IMG_0001.heic', { type: 'image/heic' });
			return await transcodeHeicFile(heic);
		} finally {
			vi.unstubAllGlobals();
		}
	}

	it('fits an oversized conversion by resampling at the first lower quality', async () => {
		const converted = await transcode(oversizedJpeg(), () => fittingJpeg());
		expect(converted.name).toBe('IMG_0001.jpg');
		expect(converted.type).toBe('image/jpeg');
		expect(converted.size).toBe(1000);
		expect(decodes).toEqual([{ width: 6000, height: 5000 }]);
		expect(draws).toEqual([{ width: 4200, height: 3500 }]);
		expect(encodedQualities).toEqual([0.7]);
	});

	it('steps down through further passes when a pass still does not fit', async () => {
		const converted = await transcode(oversizedJpeg(), (quality) =>
			quality <= 0.5 ? fittingJpeg() : oversizedJpeg());
		expect(converted.size).toBe(1000);
		// Qualities descend to the floor and each pass resamples the previous
		// output again.
		expect(encodedQualities).toEqual([0.7, 0.55, 0.5]);
		expect(draws.map((draw) => draw.width)).toEqual([4200, 2940, 2058]);
	});

	it('raises a useful failure when no pass can fit the upload cap', async () => {
		await expect(transcode(oversizedJpeg())).rejects.toThrow(OversizedConversionError);
		expect(encodedQualities).toEqual([0.7, 0.55, 0.5, 0.5]);
	});

	it('surfaces a resample failure as the actionable failure, not an HEIC fallback', async () => {
		// No canvas globals stubbed: mid-resample breakage (for example a
		// browser without OffscreenCanvas) must not fall back to uploading
		// the original HEIC, which reviewers without HEIC support cannot see.
		const { heicTo } = await import('heic-to');
		vi.mocked(heicTo).mockImplementationOnce(async () => oversizedJpeg());
		const heic = new File([ftypHeader('heic')], 'IMG_0001.heic', { type: 'image/heic' });
		await expect(transcodeHeicFile(heic)).rejects.toThrow(OversizedConversionError);
	});
});
