# Kathará Python API

This package preserves the familiar Python model API for applications that
construct Kathará labs in code. Deployment and device operations are performed
by the companion `kathara` Go binary, keeping Docker and Kubernetes client
dependencies out of Python applications.

## Install for development

Build the binary at the repository root, then install the package and point it
at that binary:

```console
mkdir -p bin
go build -o bin/kathara ./cmd/kathara
python3 -m pip install ./python
export KATHARA_BIN="$PWD/bin/kathara"
```

The package searches for the binary in this order: `KATHARA_BIN`, the active
Python environment's scripts directory, `PATH`, then `Kathara/bin` inside the
installed package.

## Example

```python
from Kathara.model.Lab import Lab
from Kathara.manager.Kathara import Kathara

lab = Lab("getting-started")
pc1 = lab.new_machine("pc1", image="kathara/base")
pc2 = lab.new_machine("pc2", image="kathara/base")
lab.connect_machine_to_link(pc1.name, "A")
lab.connect_machine_to_link(pc2.name, "A")

Kathara.get_instance().deploy_lab(lab)
```

The model, parser, settings, and exception classes remain available in Python.
The manager facade sends structured requests to the Go binary for deployment,
terminal, and execution operations.

## Limitations

Operations that cannot be represented safely through the binary interface
raise `NotSupportedError`. This includes low-level Docker/Kubernetes object
access and live resource-statistics sampling. Link-only deployment and file
transfer use the binary's internal JSON bridge. External links can be passed
through `deploy_link` or packed into `lab.ext` when deploying an in-memory lab;
they retain the Linux/root requirements described in the root README.

## Development

```console
python3 tools/vectorcheck/check_client.py
cd python && python3 -m unittest discover -s tests -t tests
```

The package is distributed under the GNU GPL v3.0.
