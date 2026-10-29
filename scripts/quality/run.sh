#!/bin/sh
set -eu
# Reuse the same source-only entrypoint locally and in CI, without a deployment build.
cd "$(dirname "$0")/../.."
make lint typecheck test
node scripts/quality/check.mjs
node --test tests/contracts/*.test.mjs web/tests/*.test.mjs
node --test tests/acceptance/acceptance.test.mjs tests/layout/model.test.mjs tests/visual-common/*.test.mjs
# Keep both map geometry contracts in the local and CI source gate.
node --test tests/treemap/*.test.mjs tests/radial/*.test.mjs
# Keep the capability matrix's fallback contracts in the standard source gate.
node --test tests/foundation/capabilities.test.mjs
python3 -B -m unittest discover -s tests/contracts -p '*_test.py'
# Validate package preflight and sealed payload rules without producing a deployment artifact.
python3 -B -m unittest discover -s tests/dsm-package -p 'test_*.py'
