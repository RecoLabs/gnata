import type { GnataConfig } from './types.js';
import { initGnata, gnataEval } from './loader.js';

export type { GnataConfig } from './types.js';

const defaultConfig: GnataConfig = {
  wasmUrl: '/wasm/gnata.wasm',
  execJsUrl: '/wasm/wasm_exec.js',
};

let config: GnataConfig = { ...defaultConfig };

/**
 * Override the default asset URLs for WASM and wasm_exec.js.
 *
 * Call before the first `jsonata()` evaluation if your assets are
 * served from a non-default path.
 */
export function configure(overrides: Partial<GnataConfig>): void {
  config = { ...config, ...overrides };
}

/**
 * Evaluate a JSONata expression against data using gnata WASM.
 *
 * API-compatible with the `jsonata` npm package's default export:
 * `jsonata(expr).evaluate(data)`.
 */
export function jsonata(expression: string): {
  evaluate(data: unknown): Promise<unknown>;
} {
  return {
    async evaluate(data: unknown) {
      await initGnata(config);
      return gnataEval(expression, data);
    },
  };
}
