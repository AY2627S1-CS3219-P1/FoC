import { describe, expect, it, vi } from 'vitest';
import { DeliveryProofController, ProofRejectedError, type DeliveryProofAPI, type ProofGrant } from './controller';

// Expected behavior comes from approved Q1/Q4/Q5/Q8/Q11 and NFR-13.
function scenario() {
	const api: DeliveryProofAPI = {
		createUpload: vi.fn(async ({ uploadId }) => ({
			proofId: uploadId, uploadUrl: 'https://storage.invalid/upload', uploadHeaders: {}
		})),
		abandonUpload: vi.fn(async () => {}),
		verifyUpload: vi.fn(async () => {}),
		confirmDelivery: vi.fn(async () => {})
	};
	const put = vi.fn(async (_grant: ProofGrant, _file: File, _signal: AbortSignal) => {});
	const controller = new DeliveryProofController('order', api, () => {}, put);
	return { api, put, controller };
}

const photo = (name = 'proof.png') => new File(['photo'], name, { type: 'image/png' });

describe('delivery proof upload and confirmation', () => {
	it('checks the object after a lost PUT response and does not confirm automatically', async () => {
		const { api, put, controller } = scenario();
		put.mockRejectedValueOnce(new Error('lost successful PUT response'));
		controller.addFiles([photo()]);
		await controller.upload();
		expect(api.verifyUpload).toHaveBeenCalledOnce();
		expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
		expect(api.confirmDelivery).not.toHaveBeenCalled();
	});

	it('retains a failed selection and successful photos and retries only the failed photo with its same upload ID', async () => {
		const { api, put, controller } = scenario();
		const failed = photo('second.png');
		controller.addFiles([photo('first.png'), failed]);
		vi.mocked(api.verifyUpload).mockResolvedValueOnce().mockRejectedValueOnce(new Error('unknown storage result'));
		await controller.upload();
		const snapshot = controller.snapshot();
		expect(snapshot.files.map((file) => file.outcome)).toEqual(['Succeeded', 'Failed']);
		expect(snapshot.files[1].file).toBe(failed);
		await controller.confirm();
		expect(api.confirmDelivery).not.toHaveBeenCalled();
		await controller.upload();
		expect(api.createUpload).toHaveBeenCalledTimes(3);
		expect(vi.mocked(api.createUpload).mock.calls[1][0].uploadId).toBe(vi.mocked(api.createUpload).mock.calls[2][0].uploadId);
		expect(put).toHaveBeenCalledTimes(3);
		await controller.confirm();
		expect(controller.snapshot().outcome).toBe('Succeeded');
		expect(vi.mocked(api.confirmDelivery).mock.calls[0][0].proofIds).toHaveLength(2);
	});

	it('requires explicit failed-photo removal before confirming the successful subset', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo('saved.png'), photo('failed.png')]);
		vi.mocked(api.verifyUpload).mockResolvedValueOnce().mockRejectedValueOnce(new Error('not confirmed'));
		await controller.upload();
		await controller.confirm();
		expect(api.confirmDelivery).not.toHaveBeenCalled();
		await controller.removeFile(controller.snapshot().files[1].id);
		await controller.confirm();
		expect(vi.mocked(api.confirmDelivery).mock.calls[0][0].proofIds).toHaveLength(1);
	});

	it('retries the identical frozen confirmation after a lost response', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo()]);
		controller.setUnattended(true);
		await controller.upload();
		vi.mocked(api.confirmDelivery).mockRejectedValueOnce(new Error('lost accepted response'));
		await controller.confirm();
		expect(controller.snapshot().outcome).toBe('Failed');
		expect(controller.snapshot().locked).toBe(true);
		controller.setUnattended(false);
		await controller.removeFile(controller.snapshot().files[0].id);
		controller.addFiles([photo('different.png')]);
		// Changing which UI button is clicked cannot change an unknown submission.
		await controller.confirm(true);
		expect(vi.mocked(api.confirmDelivery).mock.calls[1][0]).toEqual(vi.mocked(api.confirmDelivery).mock.calls[0][0]);
		expect(controller.snapshot().files).toHaveLength(1);
		expect(controller.snapshot().outcome).toBe('Succeeded');
	});

	it('distinguishes an authoritative rejection from an unknown failure', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo()]);
		await controller.upload();
		vi.mocked(api.confirmDelivery).mockRejectedValueOnce(new ProofRejectedError('Delivery deadline passed. Refresh the order.'));
		await controller.confirm();
		expect(controller.snapshot().outcome).toBe('Rejected');
		expect(controller.snapshot().message).toContain('deadline');
		expect(controller.snapshot().locked).toBe(false);
	});

	it.each([false, true])('requests server outage authorization for unattended=%s without fabricating an outage reason', async (unattended) => {
		const { api, controller } = scenario();
		controller.setUnattended(unattended);
		await controller.confirm(true);
		expect(api.confirmDelivery).toHaveBeenCalledWith({ orderId: 'order', proofIds: [], unattended, withoutProof: true }, expect.any(AbortSignal));
	});

	it('cannot bypass a rejected outage exception', async () => {
		const { api, controller } = scenario();
		vi.mocked(api.confirmDelivery).mockRejectedValueOnce(new ProofRejectedError('No upload outage is confirmed. Upload a photo.'));
		await controller.confirm(true);
		expect(controller.snapshot().outcome).toBe('Rejected');
		expect(controller.snapshot().message).toContain('Upload a photo');
	});

	it('enforces five photos and the byte/type limit without discarding prior input', () => {
		const { controller } = scenario();
		controller.addFiles(Array.from({ length: 5 }, (_, index) => photo(`${index}.png`)));
		controller.addFiles([photo('sixth.png')]);
		expect(controller.snapshot().files).toHaveLength(5);
		expect(controller.snapshot().message).toContain('at most 5');
		const other = scenario().controller;
		other.addFiles([new File(['text'], 'not-a-photo.txt', { type: 'text/plain' })]);
		expect(other.snapshot().files[0].outcome).toBe('Rejected');
		expect(other.snapshot().files[0].message).toContain('10,000,000');
	});

	it('suppresses duplicate confirmation clicks while the first is pending', async () => {
		const { api, controller } = scenario();
		controller.addFiles([photo()]);
		await controller.upload();
		let finish!: () => void;
		vi.mocked(api.confirmDelivery).mockReturnValueOnce(new Promise<void>((resolve) => { finish = resolve; }));
		const first = controller.confirm();
		await controller.confirm();
		expect(controller.snapshot().outcome).toBe('Pending');
		expect(api.confirmDelivery).toHaveBeenCalledOnce();
		finish();
		await first;
		expect(controller.snapshot().outcome).toBe('Succeeded');
	});
});

// NFR-13 retry and retained-input behavior also applies to a stalled transport.
describe('bounded delivery proof work', () => {
	it('cancels a stalled upload on unmount and does not start the next selected file', async () => {
		const api: DeliveryProofAPI = {
			createUpload: vi.fn(async ({ uploadId }) => ({ proofId: uploadId, uploadUrl: 'https://storage.invalid/upload', uploadHeaders: {} })),
			abandonUpload: vi.fn(async () => {}),
		verifyUpload: vi.fn(async () => {}),
			confirmDelivery: vi.fn(async () => {})
		};
		let started!: () => void;
		const uploading = new Promise<void>((resolve) => { started = resolve; });
		const put = vi.fn((_grant, _file, signal: AbortSignal) => {
			started();
			return new Promise<void>((_resolve, reject) => {
				signal.addEventListener('abort', () => reject(signal.reason), { once: true });
			});
		});
		const changed = vi.fn();
		const controller = new DeliveryProofController('order', api, changed, put);
		controller.addFiles([photo('first.png'), photo('second.png')]);
		const work = controller.upload();
		await uploading;
		controller.dispose();
		const previousUpdates = changed.mock.calls.length;
		await work;
		expect(api.verifyUpload).not.toHaveBeenCalled();
		expect(api.createUpload).toHaveBeenCalledOnce();
		expect(changed).toHaveBeenCalledTimes(previousUpdates);
	});

	it('bounds a stalled PUT and checks whether its unknown result was saved', async () => {
		const { api, controller, put } = scenario();
		put.mockImplementationOnce(() => new Promise<void>(() => {}));
		controller.addFiles([photo()]);
		await controller.upload();
		expect(api.verifyUpload).toHaveBeenCalledOnce();
		expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
	}, 20_000);
});

// Selected-photo removal is explicit and must release the server admission slot.
describe('acknowledged proof removal', () => {
 it('retires more than twenty selected uploads before accepting a replacement', async () => {
  const {api, controller} = scenario();
  for (let index=0;index<21;index++) {
   controller.addFiles([photo(`${index}.png`)]);
   await controller.upload();
   const id=controller.snapshot().files[0].id;
   await controller.removeFile(id);
   expect(api.abandonUpload).toHaveBeenLastCalledWith('order',id,expect.any(AbortSignal));
   expect(controller.snapshot().files).toHaveLength(0);
  }
  controller.addFiles([photo('replacement.png')]);
  await controller.upload();
  await controller.confirm();
  expect(api.abandonUpload).toHaveBeenCalledTimes(21);
  expect(controller.snapshot().outcome).toBe('Succeeded');
 });
 it('keeps unknown removals and their upload identity until the same removal succeeds', async () => {
  const {api,controller}=scenario();
  controller.addFiles([photo()]);
  vi.mocked(api.createUpload).mockRejectedValueOnce(new Error('lost grant response'));
  await controller.upload();
  const id=controller.snapshot().files[0].id;
  vi.mocked(api.abandonUpload).mockRejectedValueOnce(new Error('lost removal response'));
  await controller.removeFile(id);
  expect(controller.snapshot().files[0].id).toBe(id);
  await controller.upload();
  expect(api.createUpload).toHaveBeenCalledOnce();
  await controller.confirm();
  expect(api.confirmDelivery).not.toHaveBeenCalled();
  await controller.removeFile(id);
  expect(vi.mocked(api.abandonUpload).mock.calls[1].slice(0,2)).toEqual(['order',id]);
  expect(controller.snapshot().files).toHaveLength(0);
 });
});

// The reviewed slow-network example is an 8 MB photo on a 3 Mbps connection,
// which requires about 22 seconds. API calls still use their shorter budget.
describe('separate upload and API budgets', () => {
	it('allows an 8 MB photo to finish after the old 15-second upload cutoff', async () => {
		vi.useFakeTimers();
		const timeout = vi.spyOn(AbortSignal, 'timeout').mockImplementation((milliseconds) => {
			const controller = new AbortController();
			setTimeout(() => controller.abort(new DOMException('Timed out', 'TimeoutError')), milliseconds);
			return controller.signal;
		});
		try {
			const { api, controller, put } = scenario();
			put.mockImplementationOnce((_grant, _file, signal) => new Promise<void>((resolve, reject) => {
				setTimeout(resolve, 22_000);
				signal.addEventListener('abort', () => reject(signal.reason), { once: true });
			}));
			controller.addFiles([new File([new Uint8Array(8_000_000)], 'phone.heic', { type: 'image/heic' })]);
			const work = controller.upload();
			await vi.advanceTimersByTimeAsync(22_000);
			await work;
			expect(controller.snapshot().files[0].outcome).toBe('Succeeded');
			expect(api.verifyUpload).toHaveBeenCalledOnce();
			expect(api.confirmDelivery).not.toHaveBeenCalled();
		} finally {
			timeout.mockRestore();
			vi.clearAllTimers();
			vi.useRealTimers();
		}
	});

	it('keeps the API timeout short and retains a large photo after unknown admission', async () => {
		vi.useFakeTimers();
		const timeout = vi.spyOn(AbortSignal, 'timeout').mockImplementation((milliseconds) => {
			const controller = new AbortController();
			setTimeout(() => controller.abort(new DOMException('Timed out', 'TimeoutError')), milliseconds);
			return controller.signal;
		});
		try {
			const { api, controller, put } = scenario();
			vi.mocked(api.createUpload).mockReturnValueOnce(new Promise(() => {}));
			const file = new File([new Uint8Array(8_000_000)], 'phone.heic', { type: 'image/heic' });
			controller.addFiles([file]);
			const id = controller.snapshot().files[0].id;
			const work = controller.upload();
			await vi.advanceTimersByTimeAsync(15_000);
			await work;
			expect(put).not.toHaveBeenCalled();
			expect(controller.snapshot().files[0]).toMatchObject({ id, file, outcome: 'Failed' });
		} finally {
			timeout.mockRestore();
			vi.clearAllTimers();
			vi.useRealTimers();
		}
	});
});
