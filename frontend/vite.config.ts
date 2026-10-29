import adapter from '@sveltejs/adapter-node';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';
import { functionsMixins } from 'vite-plugin-functions-mixins';

export default defineConfig({
	// Superforms' adapter barrel also references optional server-only validators.
	// We use Zod; keep Vine out of browser resolution without polyfilling Node DNS.
	// The unused adapter is tree-shaken; server builds retain normal resolution.
	environments: {
		client: { build: { rolldownOptions: { external: ['@vinejs/vine'] } } }
	},
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			adapter: adapter()
		}),
		functionsMixins({ deps: ['m3-svelte'] })
	]
});
