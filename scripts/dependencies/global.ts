// Every global dependency whose version and checksums live in
// `dependencies.yaml`.  Imported by `scripts/rddepman.ts`.

import { Electron } from '@/scripts/dependencies/electron';
import * as tools from '@/scripts/dependencies/tools';
import { Wix } from '@/scripts/dependencies/wix';
import { VersionedDependency } from '@/scripts/lib/dependencies';

export const globalDependencies: VersionedDependency[] = [
  new tools.GoLangCILint(),
  new tools.CheckSpelling(),
  new tools.Steve(),
  new Wix(),
  new Electron(),
];
