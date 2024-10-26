# pre-commit

This repository uses [pre-commit](https://pre-commit.com/){target=_blank} to manage pre-commit hooks.

## Installation

Installing the binary is covered on the homepage of pre-commit.

To activate it for this repository:

```shell
pre-commit install
```

pre-commits are managed in the `.pre-commit-config.yaml` file.

Run a test over all files:

```shell
pre-commit run --all-files
```
