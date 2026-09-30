import { Plugin } from "vite";

//#region src/index.d.ts
declare const functionsMixins: ({
  deps,
  strip
}?: {
  deps?: string[];
  strip?: boolean;
}) => Plugin;
//#endregion
export { functionsMixins };