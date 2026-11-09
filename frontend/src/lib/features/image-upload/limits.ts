// Shared limits for direct-to-storage image uploads (review photos, profile
// pictures). Keep these aligned with the backend contracts.
export const IMAGE_CONTENT_TYPES = [
	'image/jpeg',
	'image/png',
	'image/webp',
	'image/heic',
	'image/heif'
] as const;

export type ImageContentType = (typeof IMAGE_CONTENT_TYPES)[number];

export const MAX_IMAGE_BYTES = 10_000_000;

const acceptedTypes = new Set<string>(IMAGE_CONTENT_TYPES);

export function isAcceptedImageType(value: string): value is ImageContentType {
	return acceptedTypes.has(value);
}
