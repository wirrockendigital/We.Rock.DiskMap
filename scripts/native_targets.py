"""Explicit native SPK profiles; re-read the active Control target before every build."""
import sys

TARGETS = {
    'vdsm-native-spk-test': {'arch': 'kvmx64', 'model': 'synology_kvmx64_virtualdsm', 'dsm_build': '90080'},
    'ds1621xs-production-spk': {'arch': 'broadwellnk', 'model': 'synology_broadwellnk_1621xs+', 'dsm_build': '90080'},
}


def target_profile(key):
    """Reject implicit and unknown targets instead of guessing from the build host."""
    if key not in TARGETS:
        raise ValueError('Explicit recorded native target required')
    return TARGETS[key]


if __name__ == '__main__':
    target_profile(sys.argv[1] if len(sys.argv) == 2 else '')
