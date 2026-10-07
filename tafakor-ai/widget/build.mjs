// Builds the widget into ONE self-contained script (CSS is inlined as text and
// injected into the Shadow DOM at runtime). Nothing is imported from the main app.
import { build } from 'esbuild';

await build({
  entryPoints: ['src/index.ts'],
  bundle: true,
  format: 'iife',
  target: 'es2020',
  minify: process.env.NODE_ENV !== 'development',
  sourcemap: false,
  outfile: 'dist/tafakor-ai.js',
  loader: { '.css': 'text' },
  legalComments: 'none',
  logLevel: 'info',
});
