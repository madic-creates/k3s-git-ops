# Distributed Storage

## Longhorn

[Longhorn](https://longhorn.io/){target=_blank} is used as primary local distributed storage.

### Troubleshooting

If a pod can't start and in the events is something like "Volume is already exclusively attached to one node and can't be attached to anothe" you need to wait at least 2 hours. Haven't found a solution yet.
