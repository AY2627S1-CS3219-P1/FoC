export type ImageGrant = { url: string; expiresAt: number };
export type RefreshImageGrant = (objectId: string, signal: AbortSignal) => Promise<ImageGrant>;
