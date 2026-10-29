import { checkContracts } from './contracts.mjs';
import { checkLocales } from './locales.mjs';

// The same read-only checks run in local development and CI.
checkContracts();
checkLocales();
console.log('Contract drift and translation checks passed.');
