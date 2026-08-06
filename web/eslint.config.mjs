import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTypeScript from "eslint-config-next/typescript";

export default defineConfig([
  ...nextVitals,
  ...nextTypeScript,
  {
    files: [
      "components/organization-console.tsx",
      "components/recipient-download-panel.tsx",
      "components/shipment-dashboard.tsx",
    ],
    rules: {
      // これらの画面は初回表示・選択変更時に外部APIと同期するためEffectを利用する。
      // loading/error stateを含む取得関数の呼び出しを、対象ファイルに限って許可する。
      "react-hooks/set-state-in-effect": "off",
    },
  },
  globalIgnores([".next/**", "out/**", "build/**", "next-env.d.ts"]),
]);
