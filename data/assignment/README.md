# Assignment-provided CSV data

These CSVs are supplied with the assignment and remain separate from our local
test fixtures in [`../seeds/`](../seeds/README.md). Their contents and stable source
keys are preserved when moving them from `data/csv/`.

The existing Supplier Service importer defaults to these files:

```sh
cd supplier-service
make seed
```

Set the service's `DATABASE_URL` first. This imports campus reference/location
data; it does not create test accounts, request history or closure scenarios.
