# mkdocs

This repository builds it's documentation with mkdocs.

## Installation

```shell
python3 -m venv --copies mkdocs
source mkdocs/bin/activate
# Update all python packages within the python environment
python3 -m pip list --outdated --format=json | jq -r '.[] | "\(.name)==\(.latest_version)"' | xargs --no-run-if-empty -n1 python3 -m pip install -U
# Install mkdocs requirements
pip install -r requirements.txt
```

## Building

```shell
mkdocs build --strict --verbose
mkdocs serve -a 0.0.0.0:8000
```
