// Build inputs are embedded by Vite; plain component tests and development callers may supply null.
export interface AboutMetadata {
  version: 1;
  appVersion: string;
  packageVersion: string;
  sourceId: string | null;
  components: {name: string; version: string; kind: 'ui' | 'backend' | 'toolchain'}[];
  licenses: {component: string; name: string; text: string}[];
}
declare const __DISKMAP_ABOUT__: AboutMetadata;
export const bundledAbout: AboutMetadata | null = typeof __DISKMAP_ABOUT__ === 'undefined' ? null : __DISKMAP_ABOUT__;

// These are historical, scoped probes, not runtime capabilities or universal DSM support.
export const aboutCompatibility = Object.freeze({
  platform: 'VirtualDSM 7.4.1', build: '90080',
  evidence: ['docs/testing/filestation-contract.md', 'docs/testing/scheduler-integration.md'],
});
