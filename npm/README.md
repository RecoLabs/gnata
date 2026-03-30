# gnata-js

Browser [JSONata](https://jsonata.org) evaluation via [gnata](https://github.com/RecoLabs/gnata) WebAssembly. Runs the same Go evaluator used by backend services in the browser for expression parity — not a performance optimization over the `jsonata` npm package.

## Install

```bash
npm install gnata-js
```

## Usage

```typescript
import { jsonata } from 'gnata-js';

const result = await jsonata('Account.Order.Product.Price').evaluate({
  Account: {
    Order: [
      { Product: { Price: 34.45 } },
      { Product: { Price: 21.67 } },
    ],
  },
});
// [34.45, 21.67]
```

## Asset Setup

The package ships `gnata.wasm` and `wasm_exec.js` in `node_modules/gnata-js/wasm/`. Copy them so your web server serves them at `/wasm/`:

```javascript
// rspack / webpack
new CopyPlugin({
  patterns: [{ from: 'node_modules/gnata-js/wasm', to: 'wasm' }],
});
```

Override paths with `configure()` if needed:

```typescript
import { jsonata, configure } from 'gnata-js';
configure({ wasmUrl: '/assets/gnata.wasm', execJsUrl: '/assets/wasm_exec.js' });
```

## License

MIT
