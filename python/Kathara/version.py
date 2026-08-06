from typing import Tuple

#: Placeholder: a release build takes its version from the goreleaser tag, which
#: `build_wheels.py` pins into this file. That script also refuses to publish a
#: version that does not sort above the last PyPI release (3.8.3), so this value
#: cannot reach PyPI as it stands.
CURRENT_VERSION = "1.0.0"


def parse(version: str) -> Tuple:
    return tuple([int(x) for x in version.split('.')])


def less_than(version: str, other_version: str) -> bool:
    version = parse(version)
    other_version = parse(other_version)

    return version < other_version
