declare global {
  class Go {
    importObject: WebAssembly.Imports;
    run(instance: WebAssembly.Instance): Promise<void>;
  }

  // Raw WASM exports registered by gnata's Go main() on the global object.
  // See wasm/main.go for the Go-side definitions.
  function _gnataEval(expr: string, jsonData: string): string | Error;
  function _gnataCompile(expr: string): number | Error;
  function _gnataEvalHandle(
    handle: number,
    jsonData: string,
  ): string | Error;
  function _gnataReleaseHandle(handle: number): undefined | Error;
}

export interface GnataConfig {
  wasmUrl: string;
  execJsUrl: string;
}
