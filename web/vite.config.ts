import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import {fileURLToPath} from 'node:url';
// @ts-expect-error The local Node helper is also exercised directly by its source tests.
import {createAboutMetadata} from '../scripts/about-metadata.mjs';

// Relative assets allow the same bundle to run inside the package's DSM path.
export default defineConfig({ plugins: [react()], base: './', build: { sourcemap: false },
  define: {__DISKMAP_ABOUT__: JSON.stringify(createAboutMetadata(fileURLToPath(new URL('../', import.meta.url)), {sourceId: process.env.DISKMAP_BUILD_SOURCE_ID ?? null}))},
});
