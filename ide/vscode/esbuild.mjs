import { build, context } from 'esbuild';

const watch = process.argv.includes('--watch');

const shared = {
  bundle: true,
  format: 'cjs',
  platform: 'node',
  target: 'node20',
  outbase: 'src',
  external: ['vscode', '@vscode/test-electron'],
  sourcemap: true,
  logLevel: 'info',
};

const extension = {
  ...shared,
  entryPoints: ['src/extension.ts'],
  outdir: 'dist',
  minify: !watch,
};

const tests = {
  ...shared,
  entryPoints: ['src/test/runTest.ts', 'src/test/suite/index.ts'],
  outdir: 'dist',
  minify: false,
};

if (watch) {
  const ctx = await context(extension);
  await ctx.watch();
} else {
  await build(extension);
  await build(tests);
}
