import type { GnataConfig } from './types.js';

let wasmReady = false;
let loadingPromise: Promise<void> | null = null;

function loadScript(src: string): Promise<void> {
  return new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = src;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`Failed to load ${src}`));
    document.head.appendChild(script);
  });
}

export async function initGnata(config: GnataConfig): Promise<void> {
  if (wasmReady) return;
  if (loadingPromise) return loadingPromise;

  loadingPromise = (async () => {
    try {
      if (typeof Go === 'undefined') {
        await loadScript(config.execJsUrl);
      }

      const go = new Go();
      const resp = await fetch(config.wasmUrl);
      if (!resp.ok) {
        throw new Error(
          `Failed to fetch gnata.wasm: ${resp.status} ${resp.statusText}`,
        );
      }

      const result = await WebAssembly.instantiateStreaming(
        resp,
        go.importObject,
      );
      go.run(result.instance).catch((err: unknown) => {
        console.error('gnata WASM runtime exited unexpectedly:', err);
        wasmReady = false;
        loadingPromise = null;
      });
      wasmReady = true;
    } catch (err) {
      loadingPromise = null;
      throw err instanceof Error
        ? err
        : new Error(`gnata init failed: ${String(err)}`);
    }
  })();

  return loadingPromise;
}

function unwrapWasm<T>(result: T | Error): T {
  if (result instanceof Error) throw result;
  return result;
}

export function gnataEval(expression: string, data: unknown): unknown {
  const jsonData = data != null ? JSON.stringify(data) : '';
  const raw = unwrapWasm(_gnataEval(expression, jsonData));
  // gnata returns "" for undefined (non-matching paths) and "null" for
  // actual JSON null. This preserves the jsonata npm semantic where
  // non-matching expressions return undefined.
  if (raw === '') return undefined;
  return JSON.parse(raw);
}
